package proxy_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/services/proxy"
)

// A real handshake: a test on configuration alone would pass for a proxy answering in
// the clear.

// selfSigned is one PEM blob holding a chain and its key, which is the single
// artifact the vocabulary's `certificate.secret` names.
func selfSigned(t *testing.T, host string) (blob string, pool *x509.CertPool) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: host},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{host},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	require.NoError(t, err)

	marshalled, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: marshalled})

	pool = x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(certPEM))

	// THE KEY FIRST, deliberately: the blob is split on the block type and not
	// on order, because half of them are written this way.
	return string(keyPEM) + string(certPEM), pool
}

// vault answers with whatever it was given, and records what it was asked for.
type vault struct {
	value string
	err   error
	asked string
}

func (v *vault) Secret(_ context.Context, _, _, id string) (string, error) {
	v.asked = id
	return v.value, v.err
}

// terminating is a proxy that answers the handshake itself.
func terminating(t *testing.T, res map[string]any, cfg proxy.Config, sec proxy.Secrets) (*proxy.Service, *workers) {
	t.Helper()

	w := &workers{said: "pong:"}
	opts := []proxy.Option{
		proxy.WithProjects(projects{p: compiled(t, res)}),
		proxy.WithWorkers(w),
	}
	if sec != nil {
		opts = append(opts, proxy.WithSecrets(sec))
	}

	s, err := proxy.New(cfg, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s, w
}

// speaks does a handshake and one round trip, and answers with what came back.
func speaks(t *testing.T, sock proxy.Socket, pool *x509.CertPool, serverName string) (string, error) {
	t.Helper()

	c, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second},
		"tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(sock.Port))), &tls.Config{
			RootCAs:    pool,
			ServerName: serverName,
		})
	if err != nil {
		return "", err
	}
	defer c.Close()

	c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write([]byte("ping")); err != nil {
		return "", err
	}
	if err := c.CloseWrite(); err != nil {
		return "", err
	}

	body, err := io.ReadAll(c)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return string(body), nil
}

func terminates(res map[string]any) { res["tls"] = map[string]any{"mode": "terminate"} }

func TestATlsProxyAnswersWithTheCertificateItsSecretHolds(t *testing.T) {
	res := servable()
	terminates(res)
	res["certificate"] = map[string]any{
		"source": "provided",
		"secret": map[string]any{"name": "pgcert"},
	}

	blob, pool := selfSigned(t, "db.tau.example.io")
	v := &vault{value: blob}
	s, w := terminating(t, res, proxy.Config{CloudFqdn: "tau.example.io"}, v)

	sock, err := s.Serve(t.Context(), pgwire())
	require.NoError(t, err)
	require.Equal(t, "pgcert", v.asked, "it read the secret the resource named")

	got, err := speaks(t, sock, pool, "db.tau.example.io")
	require.NoError(t, err)
	require.Equal(t, "pong:ping", got, "and the workload got plaintext")

	at, port := w.asked()
	require.Equal(t, "db", at.Name)
	require.Equal(t, "pg", port)
}

func TestAGeneratedNameTerminatesWithTheCloudsWildcard(t *testing.T) {
	res := servable()
	terminates(res)

	blob, pool := selfSigned(t, "db.tau.example.io")
	cert, key := split(t, blob)

	s, _ := terminating(t, res, proxy.Config{
		CloudFqdn: "tau.example.io",
		Wildcard:  proxy.Certificate{Certificate: cert, Key: key},
	}, nil)

	sock, err := s.Serve(t.Context(), pgwire())
	require.NoError(t, err)

	got, err := speaks(t, sock, pool, "db.tau.example.io")
	require.NoError(t, err)
	require.Equal(t, "pong:ping", got)
}

func TestTerminatingWithNoCertificateIsRefusedAndBindsNothing(t *testing.T) {
	res := servable()
	terminates(res)

	s, _ := terminating(t, res, proxy.Config{CloudFqdn: "tau.example.io"}, nil)

	_, err := s.Serve(t.Context(), pgwire())
	require.ErrorIs(t, err, proxy.ErrUnservable)
	require.ErrorContains(t, err, "holds no certificate for its own zone")
	require.Empty(t, s.Serving(), "and nothing is published")
}

func TestTheWildcardIsNotUsedForAZoneTheProjectDeclares(t *testing.T) {
	res := servable()
	terminates(res)
	res["host"] = map[string]any{
		"kind": "zone", "name": "db",
		"zone": map[string]any{"in": map[string]any{"name": zoneID}},
	}

	blob, _ := selfSigned(t, "db.tau.example.io")
	cert, key := split(t, blob)

	s, _ := terminating(t, res, proxy.Config{
		CloudFqdn: "tau.example.io",
		Wildcard:  proxy.Certificate{Certificate: cert, Key: key},
	}, nil)

	_, err := s.Serve(t.Context(), pgwire())
	require.ErrorIs(t, err, proxy.ErrUnservable)
	require.ErrorContains(t, err, "db.shop.example.com is not under")
}

func TestProvidedWithNoSecretResolverIsRefused(t *testing.T) {
	res := servable()
	terminates(res)
	res["certificate"] = map[string]any{
		"source": "provided",
		"secret": map[string]any{"name": "pgcert"},
	}

	s, _ := terminating(t, res, proxy.Config{CloudFqdn: "tau.example.io"}, nil)

	_, err := s.Serve(t.Context(), pgwire())
	require.ErrorIs(t, err, proxy.ErrUnservable)
	require.ErrorContains(t, err, "resolves no secret")
}

func TestASecretMissingEitherHalfIsRefusedByName(t *testing.T) {
	blob, _ := selfSigned(t, "db.tau.example.io")
	cert, key := split(t, blob)

	for _, c := range []struct {
		name, value, says string
	}{
		{"only the chain", cert, "holds no private key"},
		{"only the key", key, "holds no certificate"},
		{"neither", "not pem at all", "holds no certificate"},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := servable()
			terminates(res)
			res["certificate"] = map[string]any{
				"source": "provided",
				"secret": map[string]any{"name": "pgcert"},
			}

			s, _ := terminating(t, res, proxy.Config{CloudFqdn: "tau.example.io"}, &vault{value: c.value})

			_, err := s.Serve(t.Context(), pgwire())
			require.ErrorIs(t, err, proxy.ErrUnservable)
			require.ErrorContains(t, err, c.says)
		})
	}
}

func TestAProxyThatTerminatesNothingNeedsNoCertificate(t *testing.T) {
	s, _ := terminating(t, servable(), proxy.Config{CloudFqdn: "tau.example.io"}, nil)

	sock, err := s.Serve(t.Context(), pgwire())
	require.NoError(t, err)
	require.NotZero(t, sock.Port)

	// The deadline is the assertion: a plaintext proxy forwards the ClientHello and the
	// handshake never completes.
	_, err = tls.DialWithDialer(&net.Dialer{Timeout: time.Second},
		"tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(sock.Port))), &tls.Config{InsecureSkipVerify: true})
	require.Error(t, err, "there is no handshake to have")
}

// split pulls the two halves back out of one blob, for the tests that need
// them separately.
func split(t *testing.T, blob string) (cert, key string) {
	t.Helper()

	rest := []byte(blob)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			cert += string(pem.EncodeToMemory(block))
			continue
		}
		key += string(pem.EncodeToMemory(block))
	}
	require.NotEmpty(t, cert)
	require.NotEmpty(t, key)
	return cert, key
}
