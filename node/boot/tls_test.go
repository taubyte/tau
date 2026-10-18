package boot

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/node"
	"github.com/taubyte/tau/pkg/certificate"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/services/gateway"
)

// tls_test is a REAL HANDSHAKE AGAINST THE NODE'S OWN LISTENER: the file names
// an https address and the cloud's wildcard, a project with a generated
// gateway is published, and a TLS client dialling that name is answered with
// the wildcard and reaches the gateway. A test of configuration alone would
// pass for a node that bound the address in the clear.

func wildcardFor(t *testing.T, name string) (certificate.Pair, *x509.CertPool) {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{name},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &private.PublicKey, private)
	require.NoError(t, err)
	marshalled, err := x509.MarshalECPrivateKey(private)
	require.NoError(t, err)

	p := certificate.Pair{
		Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		Key:         string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: marshalled})),
	}
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM([]byte(p.Certificate)))
	return p, pool
}

// fixtureWithHosts is the vocabulary's project and the names it answers on
// under the cloud's zone, and the last of its generated gateway names.
func fixtureWithHosts(t *testing.T, cloud string) ([]byte, []string, string) {
	t.Helper()
	c, err := schema.New(schema.WithLocal("../../pkg/tcc/taubyte/v2/fixtures/basic"), schema.WithBranch("main"))
	require.NoError(t, err)
	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)
	compiled, err := json.Marshal(obj.Flat())
	require.NoError(t, err)

	p, err := specs.Load(compiled)
	require.NoError(t, err)
	var hosts []string
	var generated string
	for _, h := range p.Hosts() {
		if !h.Generated {
			hosts = append(hosts, h.Fqdn)
			continue
		}
		hosts = append(hosts, h.Fqdn+"."+cloud)
		if h.Kind == specs.KindGateway {
			generated = h.Fqdn + "." + cloud
		}
	}
	require.NotEmpty(t, generated)
	return compiled, hosts, generated
}

// TestTheNodeTerminatesAGeneratedNameWithTheCloudsWildcard.
func TestTheNodeTerminatesAGeneratedNameWithTheCloudsWildcard(t *testing.T) {
	const cloud = "tau.example.io"
	compiled, hosts, name := fixtureWithHosts(t, cloud)
	wildcard, pool := wildcardFor(t, name)

	f := File{
		Node: node.Config{
			Services: []string{node.Register, node.Stats, node.Gateway},
			Gateway:  gateway.Config{CloudFqdn: cloud, Wildcard: wildcard},
		},
		Root:     t.TempDir(),
		Listen:   []string{"/ip4/127.0.0.1/tcp/0"},
		SwarmKey: base64.StdEncoding.EncodeToString(swarmKey()),
		HTTP:     "127.0.0.1:0",
		HTTPS:    "127.0.0.1:0",
	}
	running, err := Start(t.Context(), f)
	require.NoError(t, err)
	defer running.Close()
	require.NotEmpty(t, running.Address(), "the clear listener is still bound")
	require.NotEmpty(t, running.SecureAddress())

	_, err = running.Node.Register().Local().Publish(t.Context(), register.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled, Hosts: hosts,
	})
	require.NoError(t, err)

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: name}}}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+running.SecureAddress()+"/", nil)
	require.NoError(t, err)
	req.Host = name

	res, err := client.Do(req)
	require.NoError(t, err, "the handshake verified the wildcard against the name")
	defer res.Body.Close()
	_, err = io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, name, res.TLS.PeerCertificates[0].DNSNames[0])
	require.NotEqual(t, http.StatusNotFound, res.StatusCode, "the gateway found its route for the name over TLS")

	// A NAME THE WILDCARD DOES NOT COVER fails its handshake rather than being
	// answered with a certificate for something else.
	_, err = tls.Dial("tcp", running.SecureAddress(), &tls.Config{RootCAs: pool, ServerName: "shop.example.com"})
	require.Error(t, err)
}

// TestTheHTTPSAddressIsInTheFile.
func TestTheHTTPSAddressIsInTheFile(t *testing.T) {
	out, err := yaml.Marshal(File{HTTPS: ":8443", SwarmKey: "c3dhcm0="})
	require.NoError(t, err)
	back, err := Read(write(t, string(out)))
	require.NoError(t, err)
	require.Equal(t, ":8443", back.HTTPS)
}

// TestAnHTTPSAddressAlreadyTakenIsAnError, and the node is not left half up.
func TestAnHTTPSAddressAlreadyTakenIsAnError(t *testing.T) {
	first, err := Start(t.Context(), File{
		Node:     node.Config{Services: []string{node.Register, node.Stats, node.Gateway}},
		Root:     t.TempDir(),
		Listen:   []string{"/ip4/127.0.0.1/tcp/0"},
		SwarmKey: base64.StdEncoding.EncodeToString(swarmKey()),
		HTTPS:    "127.0.0.1:0",
	})
	require.NoError(t, err)
	defer first.Close()
	require.Empty(t, first.Address(), "no clear listener was asked for")

	_, err = Start(t.Context(), File{
		Node:     node.Config{Services: []string{node.Register, node.Stats, node.Gateway}},
		Root:     t.TempDir(),
		Listen:   []string{"/ip4/127.0.0.1/tcp/0"},
		SwarmKey: base64.StdEncoding.EncodeToString(swarmKey()),
		HTTPS:    first.SecureAddress(),
	})
	require.ErrorContains(t, err, "listening on")
}
