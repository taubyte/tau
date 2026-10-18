package gateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/certificate"
	"github.com/taubyte/tau/pkg/specs"
)

// tls_test holds which certificate answers a gateway's name, and ends in a
// real handshake: what a certificate block means is that a TLS client reaches
// the route, and a test asserting on configuration alone would pass for a
// gateway answering in the clear.

// issued is a certificate for the names given, as the PEM halves and as the
// pool a client trusts it through.
func issued(t *testing.T, names ...string) (cert, key string, pool *x509.CertPool) {
	t.Helper()

	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: names[0]},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              names,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &private.PublicKey, private)
	require.NoError(t, err)
	marshalled, err := x509.MarshalECPrivateKey(private)
	require.NoError(t, err)

	cert = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	key = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: marshalled}))
	pool = x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM([]byte(cert)))
	return cert, key, pool
}

// generatedName is the fixture's generated gateway name under the cloud's zone.
func generatedName(t *testing.T) string {
	t.Helper()
	p, err := specs.Load(compiled(t))
	require.NoError(t, err)
	// THE LAST, as TestAGeneratedNameLosesTheCloudsSegment takes it: the
	// fixture's other generated gateway is guarded, and this is about the
	// handshake rather than the guard.
	var name string
	for _, host := range p.Hosts() {
		if host.Generated && host.Kind == gatewayKind {
			name = host.Fqdn + ".tau.example.io"
		}
	}
	require.NotEmpty(t, name, "the fixture has no generated gateway name")
	return name
}

// providing is the fixture with its shop gateway providing a certificate.
func providing(t *testing.T) []byte {
	t.Helper()
	var flat map[string]any
	require.NoError(t, json.Unmarshal(compiled(t), &flat))

	root := flat["object"].(map[string]any)
	var found bool
	for _, v := range root[gatewayKind].(map[string]any) {
		gw := v.(map[string]any)
		if gw["name"] != "shop" {
			continue
		}
		found = true
		gw["certificate"] = map[string]any{"source": "provided", "secret": map[string]any{"name": "Qmshopcert", "namespace": "/shop"}}
	}
	require.True(t, found)

	b, err := json.Marshal(flat)
	require.NoError(t, err)
	return b
}

type vault struct {
	value string
	err   error
}

func (v vault) Secret(context.Context, string, string, string) (string, error) { return v.value, v.err }

func hello(name string) *tls.ClientHelloInfo { return &tls.ClientHelloInfo{ServerName: name} }

func leafOf(t *testing.T, c *tls.Certificate) string {
	t.Helper()
	require.NotNil(t, c)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Certificate[0]}))
}

// TestAGeneratedNameAnswersWithTheCloudsWildcard — the default on every
// gateway, so it is the common case and must never be a refusal.
func TestAGeneratedNameAnswersWithTheCloudsWildcard(t *testing.T) {
	name := generatedName(t)
	cert, key, _ := issued(t, name)
	h := newHarness(t, Config{CloudFqdn: "tau.example.io", Wildcard: certificate.Pair{Certificate: cert, Key: key}})

	got, err := h.Gateway.Certificate(hello(name + "."))
	require.NoError(t, err)
	require.Equal(t, cert, leafOf(t, got))
}

// TestANameTheWildcardDoesNotCoverFailsItsHandshakeAndNothingElse. shop is in
// the author's zone, on the defaulted `generated`, and this cloud issues
// nothing: the handshake fails, and the name is still served in the clear.
func TestANameTheWildcardDoesNotCoverFailsItsHandshakeAndNothingElse(t *testing.T) {
	cert, key, _ := issued(t, "x.tau.example.io")
	h := newHarness(t, Config{CloudFqdn: "tau.example.io", Wildcard: certificate.Pair{Certificate: cert, Key: key}})

	_, err := h.Gateway.Certificate(hello("shop.example.com"))
	require.ErrorIs(t, err, ErrNoCertificate)
	require.ErrorIs(t, err, certificate.ErrNotCovered)

	w := h.do(t, http.MethodPost, "http://shop.example.com/checkout", "")
	require.Equal(t, http.StatusOK, w.Code, "a default asked for nothing a plain request breaks")

	// AND A NODE WITH NO WILDCARD AT ALL says that, not something else.
	bare := newHarness(t, Config{CloudFqdn: "tau.example.io"})
	_, err = bare.Gateway.Certificate(hello(generatedName(t)))
	require.ErrorIs(t, err, certificate.ErrNoWildcard)
}

// TestAHandshakeForNothingIsRefused: no server name, a name nobody owns, and a
// name its owner serves nothing at.
func TestAHandshakeForNothingIsRefused(t *testing.T) {
	h := newHarness(t, Config{CloudFqdn: "tau.example.io"})

	_, err := h.Gateway.Certificate(hello(""))
	require.ErrorIs(t, err, ErrNoServerName)

	_, err = h.Gateway.Certificate(hello("nobody.example.com"))
	require.ErrorIs(t, err, ErrNoCertificate)
}

// TestAProvidedCertificateIsTheSecret, read through the resolver, and refused
// by name when it cannot be.
func TestAProvidedCertificateIsTheSecret(t *testing.T) {
	cert, key, _ := issued(t, "shop.example.com")

	h := newHarnessOf(t, providing(t), Config{}, WithSecrets(vault{value: key + cert}))
	got, err := h.Gateway.Certificate(hello("shop.example.com"))
	require.NoError(t, err)
	require.Equal(t, cert, leafOf(t, got))

	h = newHarnessOf(t, providing(t), Config{})
	_, err = h.Gateway.Certificate(hello("shop.example.com"))
	require.ErrorContains(t, err, "resolves no secret")

	h = newHarnessOf(t, providing(t), Config{}, WithSecrets(vault{err: errors.New("vault sealed")}))
	_, err = h.Gateway.Certificate(hello("shop.example.com"))
	require.ErrorContains(t, err, "reading the secret in /shop: vault sealed")

	h = newHarnessOf(t, providing(t), Config{}, WithSecrets(vault{value: cert}))
	_, err = h.Gateway.Certificate(hello("shop.example.com"))
	require.ErrorIs(t, err, certificate.ErrNoKey)
}

// TestTHECERTIFICATEBLOCKITSELF, where the compile could not have produced it.
func TestACertificateBlockThisCannotProduceIsRefused(t *testing.T) {
	s := &Service{}
	_, err := s.certificateOf(t.Context(), specs.Certificate{Source: "provided"}, "shop", "prod", "x")
	require.ErrorContains(t, err, "names no secret")
	_, err = s.certificateOf(t.Context(), specs.Certificate{Source: "acme"}, "shop", "prod", "x")
	require.ErrorContains(t, err, `source is "acme"`)
	require.Empty(t, scopeOf(""))
}

// TestAPROVIDEDCERTIFICATEISREFUSEDINTHECLEARWHERENOTHINGTERMINATES, and served
// in the clear where something does — as any site is.
func TestAProvidedCertificateIsRefusedInTheClearWhereNothingTerminates(t *testing.T) {
	h := newHarnessOf(t, providing(t), Config{})
	w := h.do(t, http.MethodPost, "http://shop.example.com/checkout", "")
	require.Equal(t, http.StatusMisdirectedRequest, w.Code)
	require.Contains(t, w.Body.String(), "provides its own certificate")
	require.Empty(t, h.Workers.seen(), "nothing was invoked for it")

	h = newHarnessOf(t, providing(t), Config{}, WithTLS())
	w = h.do(t, http.MethodPost, "http://shop.example.com/checkout", "")
	require.Equal(t, http.StatusOK, w.Code)
}

// TestARealHandshakeReachesTheRoute: a TLS client, the certificate the
// gateway chose, verified against the name, and the answer the route gave.
func TestARealHandshakeReachesTheRoute(t *testing.T) {
	name := generatedName(t)
	cert, key, pool := issued(t, name)
	h := newHarness(t, Config{CloudFqdn: "tau.example.io", Wildcard: certificate.Pair{Certificate: cert, Key: key}}, WithTLS())

	srv := httptest.NewUnstartedServer(h.Gateway.Handler())
	srv.TLS = &tls.Config{GetCertificate: h.Gateway.Certificate, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: name}}}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/", nil)
	require.NoError(t, err)
	req.Host = name

	res, err := client.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, res.StatusCode, string(body))
	require.Equal(t, "website/shop", res.Header.Get("X-Served"))
	require.Equal(t, name, res.TLS.PeerCertificates[0].DNSNames[0], "the wildcard the gateway chose")
}
