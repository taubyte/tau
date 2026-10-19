package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/letsencrypt/pebble/v2/ca"
	"github.com/letsencrypt/pebble/v2/db"
	"github.com/letsencrypt/pebble/v2/va"
	"github.com/letsencrypt/pebble/v2/wfe"
	mdns "github.com/miekg/dns"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/certificate"
)

// The CA here is pebble, in process; nothing about ACME is faked. Its
// validation authority resolves every name to loopback and fetches the answer
// from a listener serving Issuer.Challenge, the way a gateway does.

type cloud struct {
	directory string
	rootCA    string
	ca        *ca.CAImpl
	answer    func(token string) (string, bool)
}

func loopbackResolver(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &mdns.Server{Listener: l, Handler: mdns.HandlerFunc(func(w mdns.ResponseWriter, r *mdns.Msg) {
		m := new(mdns.Msg)
		m.SetReply(r)
		if q := r.Question[0]; q.Qtype == mdns.TypeA {
			m.Answer = []mdns.RR{&mdns.A{Hdr: mdns.RR_Header{Name: q.Name, Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: 1}, A: net.IPv4(127, 0, 0, 1)}}
		}
		w.WriteMsg(m)
	})}
	go srv.ActivateAndServe()
	t.Cleanup(func() { srv.Shutdown() })
	return l.Addr().String()
}

// newCloud starts pebble; eab, when set, is required of every account.
func newCloud(t *testing.T, eab map[string]string) *cloud {
	t.Helper()
	t.Setenv("PEBBLE_VA_NOSLEEP", "1")
	// NO NONCE IS REJECTED: pebble refuses 5% of good nonces by default, and each
	// refusal costs the client an exponential backoff of up to ten seconds, so a
	// run that drew a few spent its whole wait on them. Retrying a bad nonce is the
	// ACME client's job, not what these tests are about.
	t.Setenv("PEBBLE_WFE_NONCEREJECT", "0")
	// EVERY ORDER IS PROVED: pebble otherwise reuses a valid authorization at
	// random, so whether a renewal reaches the challenge would be chance.
	t.Setenv("PEBBLE_AUTHZREUSE", "0")
	c := &cloud{}
	challenges := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, _ := strings.CutPrefix(r.URL.Path, ChallengePrefix)
		answer, ok := c.answer(token)
		if !ok {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, answer)
	}))
	t.Cleanup(challenges.Close)
	_, port, err := net.SplitHostPort(challenges.Listener.Addr().String())
	require.NoError(t, err)
	httpPort, err := strconv.Atoi(port)
	require.NoError(t, err)

	logger := log.New(io.Discard, "", 0)
	store := db.NewMemoryStore()
	for kid, key := range eab {
		require.NoError(t, store.AddExternalAccountKeyByID(kid, key))
	}
	c.ca = ca.New(logger, store, "", "ecdsa", 0, 1, map[string]ca.Profile{"default": {Description: "default"}})
	v := va.New(logger, httpPort, 0, false, loopbackResolver(t), store)
	front := wfe.New(logger, store, v, c.ca, nil, false, len(eab) > 0, 0, 0)
	srv := httptest.NewTLSServer(front.Handler())
	t.Cleanup(srv.Close)
	c.directory = srv.URL + "/dir"
	c.rootCA = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
	return c
}

func (c *cloud) issuer(t *testing.T, cfg certificate.ACME, dir string, opts ...Option) *Issuer {
	t.Helper()
	cfg.Enabled, cfg.Directory = true, c.directory
	if !cfg.InsecureSkipVerify {
		cfg.RootCA = c.rootCA
	}
	i, err := New(cfg, dir, opts...)
	require.NoError(t, err)
	c.answer = i.Challenge
	return i
}

func (c *cloud) verify(t *testing.T, cert []byte, name string) {
	t.Helper()
	leaf, err := x509.ParseCertificate(cert)
	require.NoError(t, err)
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(c.ca.GetRootCert(0).PEM()))
	_, err = leaf.Verify(x509.VerifyOptions{DNSName: name, Roots: roots, Intermediates: intermediates(c)})
	require.NoError(t, err)
}

func intermediates(c *cloud) *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(c.ca.GetIntermediateCert(0).PEM())
	return pool
}

func TestIssuesKeepsAndReloads(t *testing.T) {
	c := newCloud(t, nil)
	dir := t.TempDir()
	i := c.issuer(t, certificate.ACME{Email: "ops@example.io"}, dir)

	cert, err := i.Certificate(t.Context(), "A.Shop.Example.IO.")
	require.NoError(t, err)
	c.verify(t, cert.Certificate[0], "a.shop.example.io")

	again, err := i.Certificate(t.Context(), "a.shop.example.io")
	require.NoError(t, err)
	require.Equal(t, cert.Certificate[0], again.Certificate[0], "a kept certificate is not reissued")

	// A NEW ISSUER OVER THE SAME DIRECTORY reloads it, and the minted account key.
	reloaded := c.issuer(t, certificate.ACME{}, dir)
	got, err := reloaded.Certificate(t.Context(), "a.shop.example.io")
	require.NoError(t, err)
	require.Equal(t, cert.Certificate[0], got.Certificate[0])
	require.Equal(t, i.client.Key.Public(), reloaded.client.Key.Public())
}

func TestADueCertificateIsServedAndRenewed(t *testing.T) {
	c := newCloud(t, nil)
	now := time.Now()
	i := c.issuer(t, certificate.ACME{}, t.TempDir(), WithClock(func() time.Time { return now }))
	first, err := i.Certificate(t.Context(), "r.example.io")
	require.NoError(t, err)

	// Two thirds of its life later it is still served, and replaced behind it.
	now = first.Leaf.NotBefore.Add(first.Leaf.NotAfter.Sub(first.Leaf.NotBefore) * 3 / 4)
	served, err := i.Certificate(t.Context(), "r.example.io")
	require.NoError(t, err)
	require.Equal(t, first.Certificate[0], served.Certificate[0])
	require.Eventually(t, func() bool {
		next, err := i.Certificate(t.Context(), "r.example.io")
		return err == nil && string(next.Certificate[0]) != string(first.Certificate[0])
	}, 30*time.Second, 50*time.Millisecond)

	// Past its end, a certificate is not served at all: it is reissued.
	now = now.Add(10 * 365 * 24 * time.Hour)
	require.Nil(t, i.kept("r.example.io"))
}

func TestAnExternalAccountBindingIsPresented(t *testing.T) {
	mac := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	c := newCloud(t, map[string]string{"kid-1": mac})

	_, err := c.issuer(t, certificate.ACME{}, t.TempDir()).Certificate(t.Context(), "e.example.io")
	require.ErrorContains(t, err, "registering the account", "the CA requires a binding")

	i := c.issuer(t, certificate.ACME{EAB: certificate.EAB{KeyID: "kid-1", HMAC: mac + "="}}, t.TempDir())
	_, err = i.Certificate(t.Context(), "e.example.io")
	require.NoError(t, err)
}

func TestInsecureSkipVerifyReachesAnUntrustedDirectory(t *testing.T) {
	c := newCloud(t, nil)
	_, err := New(certificate.ACME{Enabled: true, Directory: c.directory}, t.TempDir())
	require.NoError(t, err)

	i := c.issuer(t, certificate.ACME{InsecureSkipVerify: true}, t.TempDir())
	_, err = i.Certificate(t.Context(), "s.example.io")
	require.NoError(t, err)

	// Trusting neither, the directory is refused.
	cfg := certificate.ACME{Enabled: true, Directory: c.directory}
	untrusted, err := New(cfg, t.TempDir())
	require.NoError(t, err)
	_, err = untrusted.Certificate(t.Context(), "s.example.io")
	require.Error(t, err)
}

func TestAHandshakeGivesUpWithoutStoppingTheIssuance(t *testing.T) {
	c := newCloud(t, nil)
	i := c.issuer(t, certificate.ACME{}, t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := i.Certificate(ctx, "c.example.io")
	require.ErrorIs(t, err, context.Canceled)
}

func TestWhatIsRefused(t *testing.T) {
	dir := t.TempDir()
	_, err := New(certificate.ACME{}, dir)
	require.ErrorIs(t, err, ErrDisabled)
	_, err = New(certificate.ACME{Enabled: true}, "")
	require.ErrorIs(t, err, ErrNoDir)
	_, err = New(certificate.ACME{Enabled: true}, filepath.Join(dir, "\x00"))
	require.Error(t, err)

	for name, cfg := range map[string]certificate.ACME{
		"not pem":         {AccountKey: "nope"},
		"unknown type":    {AccountKey: string(pem.EncodeToMemory(&pem.Block{Type: "DSA PRIVATE KEY", Bytes: []byte{1}}))},
		"ed25519":         {AccountKey: pkcs8(t, ed25519Key(t))},
		"bad pkcs8":       {AccountKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1}}))},
		"half an eab":     {EAB: certificate.EAB{KeyID: "kid"}},
		"eab not base64":  {EAB: certificate.EAB{KeyID: "kid", HMAC: "*"}},
		"root ca nothing": {RootCA: "nope"},
	} {
		cfg.Enabled = true
		_, err := New(cfg, t.TempDir())
		require.Error(t, err, name)
	}

	// A kept account key that no longer parses is an error, not a new account.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "account.key"), []byte("garbage"), 0600))
	_, err = New(certificate.ACME{Enabled: true}, dir)
	require.Error(t, err)

	i, err := New(certificate.ACME{Enabled: true}, t.TempDir())
	require.NoError(t, err)
	for _, name := range []string{"", ".x.io", "a..b", "a/b", strings.Repeat("a", 254)} {
		_, err := i.Certificate(t.Context(), name)
		require.ErrorContains(t, err, "not a name", name)
	}
	_, ok := i.Challenge("a/b")
	require.False(t, ok)
	_, ok = i.Challenge("")
	require.False(t, ok)
	answer, ok := i.Challenge("tok-en_1")
	require.True(t, ok)
	require.True(t, strings.HasPrefix(answer, "tok-en_1."))
}

func TestEveryAccountKeyFormParses(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	ecDER, err := x509.MarshalECPrivateKey(ecKey)
	require.NoError(t, err)
	for name, key := range map[string]string{
		"pkcs1": string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey)})),
		"sec1":  string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: ecDER})),
		"pkcs8": pkcs8(t, ecKey),
	} {
		i, err := New(certificate.ACME{Enabled: true, AccountKey: key}, t.TempDir())
		require.NoError(t, err, name)
		require.NotNil(t, i.client.Key, name)
	}
}

func TestAKeptFileThatIsNotACertificateIsReissued(t *testing.T) {
	c := newCloud(t, nil)
	dir := t.TempDir()
	i := c.issuer(t, certificate.ACME{}, dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "k.example.io.pem"), []byte("garbage"), 0600))
	cert, err := i.Certificate(t.Context(), "k.example.io")
	require.NoError(t, err)
	c.verify(t, cert.Certificate[0], "k.example.io")
}

func ed25519Key(t *testing.T) ed25519.PrivateKey {
	_, k, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return k
}

func pkcs8(t *testing.T, key any) string {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func TestAChallengeTheCADoesNotSeeAnsweredFails(t *testing.T) {
	c := newCloud(t, nil)
	i := c.issuer(t, certificate.ACME{}, t.TempDir())
	c.answer = func(string) (string, bool) { return "", false }
	_, err := i.Certificate(t.Context(), "p.example.io")
	require.ErrorContains(t, err, "proving p.example.io")
}

func TestAnIssuanceIsBounded(t *testing.T) {
	c := newCloud(t, nil)
	i := c.issuer(t, certificate.ACME{}, t.TempDir(), WithTimeout(time.Nanosecond))
	_, err := i.Certificate(t.Context(), "t.example.io")
	require.Error(t, err)
}

func TestAFailedRenewalIsSaidAndTheOldCertificateKeepsServing(t *testing.T) {
	c := newCloud(t, nil)
	now := time.Now()
	said := make(chan string, 1)
	i := c.issuer(t, certificate.ACME{}, t.TempDir(), WithClock(func() time.Time { return now }),
		WithLogger(func(format string, args ...any) { said <- format }))
	first, err := i.Certificate(t.Context(), "f.example.io")
	require.NoError(t, err)

	c.answer = func(string) (string, bool) { return "", false }
	now = first.Leaf.NotAfter.Add(-time.Minute)
	served, err := i.Certificate(t.Context(), "f.example.io")
	require.NoError(t, err)
	require.Equal(t, first.Certificate[0], served.Certificate[0])
	select {
	case format := <-said:
		require.Contains(t, format, "renewing")
	case <-time.After(30 * time.Second):
		t.Fatal("a failed renewal was not said")
	}
}

func TestADirectoryThatCannotBeWrittenIsAnError(t *testing.T) {
	c := newCloud(t, nil)
	dir := t.TempDir()
	i := c.issuer(t, certificate.ACME{}, dir)
	// The file's temporary name is taken by a directory, so it cannot be written.
	require.NoError(t, os.Mkdir(filepath.Join(dir, "w.example.io.pem.tmp"), 0700))
	_, err := i.Certificate(t.Context(), "w.example.io")
	require.Error(t, err)

	file := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(file, nil, 0600))
	_, err = New(certificate.ACME{Enabled: true}, filepath.Join(file, "sub"))
	require.Error(t, err)
}
