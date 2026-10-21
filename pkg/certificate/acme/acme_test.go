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
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	txt       *presenter
}

// presenter is a zone's TXT records in memory, what the resolver answers
// DNS-01 lookups from.
type presenter struct {
	mu      sync.Mutex
	records map[string][]string
	fail    error
}

func (p *presenter) Present(_ context.Context, fqdn, value string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail != nil {
		return p.fail
	}
	p.records[fqdn] = append(p.records[fqdn], value)
	return nil
}

func (p *presenter) CleanUp(_ context.Context, fqdn, value string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.records[fqdn] = slices.DeleteFunc(p.records[fqdn], func(v string) bool { return v == value })
	return nil
}

func (p *presenter) lookup(fqdn string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.records[fqdn])
}

func loopbackResolver(t *testing.T, txt *presenter) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &mdns.Server{Listener: l, Handler: mdns.HandlerFunc(func(w mdns.ResponseWriter, r *mdns.Msg) {
		m := new(mdns.Msg)
		m.SetReply(r)
		q := r.Question[0]
		switch q.Qtype {
		case mdns.TypeA:
			m.Answer = []mdns.RR{&mdns.A{Hdr: mdns.RR_Header{Name: q.Name, Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: 1}, A: net.IPv4(127, 0, 0, 1)}}
		case mdns.TypeTXT:
			fqdn := strings.TrimSuffix(strings.TrimPrefix(q.Name, "_acme-challenge."), ".")
			for _, v := range txt.lookup(fqdn) {
				m.Answer = append(m.Answer, &mdns.TXT{Hdr: mdns.RR_Header{Name: q.Name, Rrtype: mdns.TypeTXT, Class: mdns.ClassINET, Ttl: 1}, Txt: []string{v}})
			}
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
	c := &cloud{txt: &presenter{records: map[string][]string{}}}
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
	v := va.New(logger, httpPort, 0, false, loopbackResolver(t, c.txt), store)
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
	require.Nil(t, i.kept(t.Context(), "r.example.io"))
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
	require.ErrorIs(t, Check(certificate.ACME{}), ErrDisabled)
	require.NoError(t, Check(certificate.ACME{Enabled: true}))
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
		require.Error(t, Check(cfg), name)
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

// memStore is a Store in memory, as services/globals' is to the issuer.
type memStore struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

func (m *memStore) Load(_ context.Context, name string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b, ok := m.blobs[name]; ok {
		return b, nil
	}
	return nil, ErrMiss
}

func (m *memStore) Keep(_ context.Context, name string, blob []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blobs[name] = blob
	return nil
}

func TestTheZoneItsNamesAndItsWildcardAreProvedByDNS01(t *testing.T) {
	c := newCloud(t, nil)
	key, _, err := MintKey()
	require.NoError(t, err)
	store := &memStore{blobs: map[string][]byte{}}
	// NO DIRECTORY: the store and the key were both given.
	i := c.issuer(t, certificate.ACME{}, "", WithStore(store), WithAccountKey(key), WithDNS("Tau.Example.IO.", c.txt))
	// HTTP-01 answers nothing, so only DNS-01 can have proved these.
	c.answer = func(string) (string, bool) { return "", false }

	for _, name := range []string{"*.tau.example.io", "build-frontend.tau.example.io", "tau.example.io"} {
		cert, err := i.Certificate(t.Context(), name)
		require.NoError(t, err, name)
		c.verify(t, cert.Certificate[0], strings.Replace(name, "*", "anything", 1))
		require.Contains(t, store.blobs, name, "kept in the store given")
	}
	require.Empty(t, c.txt.lookup("tau.example.io"), "every record is withdrawn once its challenge is decided")

	// A NAME OUTSIDE THE ZONE is still proved over HTTP-01.
	c.answer = i.Challenge
	cert, err := i.Certificate(t.Context(), "shop.example.com")
	require.NoError(t, err)
	c.verify(t, cert.Certificate[0], "shop.example.com")
}

func TestAPresenterThatFailsFailsTheIssuance(t *testing.T) {
	c := newCloud(t, nil)
	failing := &presenter{records: map[string][]string{}, fail: errors.New("no dns node")}
	i := c.issuer(t, certificate.ACME{}, t.TempDir(), WithDNS("tau.example.io", failing))
	_, err := i.Certificate(t.Context(), "*.tau.example.io")
	require.ErrorContains(t, err, "no dns node")

	// AND A WILDCARD WITHOUT DNS-01 is refused by the CA's offer, by name.
	plain := c.issuer(t, certificate.ACME{}, t.TempDir())
	_, err = plain.Certificate(t.Context(), "*.tau.example.io")
	require.ErrorContains(t, err, "no http-01 challenge")
}

func TestAThumbprintAloneAnswersHTTP01(t *testing.T) {
	i, err := New(certificate.ACME{Enabled: true}, t.TempDir())
	require.NoError(t, err)
	thumb, err := i.Thumbprint()
	require.NoError(t, err)
	want, ok := i.Challenge("tok")
	require.True(t, ok)
	got, ok := Answer(thumb, "tok")
	require.True(t, ok)
	require.Equal(t, want, got)
	_, ok = Answer(thumb, "a/b")
	require.False(t, ok)
	_, ok = Answer("", "tok")
	require.False(t, ok)

	_, err = ParseKey([]byte("nope"))
	require.Error(t, err)
}

func TestIssueOrdersAgainWhateverIsKept(t *testing.T) {
	c := newCloud(t, nil)
	i := c.issuer(t, certificate.ACME{}, t.TempDir())
	first, err := i.Certificate(t.Context(), "again.example.io")
	require.NoError(t, err)
	second, err := i.Issue("Again.Example.IO.")
	require.NoError(t, err)
	require.NotEqual(t, first.Leaf.SerialNumber, second.Leaf.SerialNumber)
	_, err = i.Issue("a/b")
	require.ErrorContains(t, err, "not a name")
}

// answers is an HTTPPresenter in memory, what every gateway would read.
type answers struct {
	mu     sync.Mutex
	tokens map[string]string
}

func (a *answers) PresentHTTP(_ context.Context, token, keyAuth string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tokens[token] = keyAuth
	return nil
}

func (a *answers) CleanUpHTTP(_ context.Context, token, keyAuth string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tokens[token] == keyAuth {
		delete(a.tokens, token)
	}
	return nil
}

func (a *answers) answer(token string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	v, ok := a.tokens[token]
	return v, ok
}

// confirmer holds back n times, then confirms what it is asked about while
// held is true.
type confirmer struct {
	mu    sync.Mutex
	n     int
	asked []string
	held  func(kind, name, value string) bool
}

func (c *confirmer) Confirmed(_ context.Context, kind, name, value string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked = append(c.asked, kind)
	if c.n > 0 {
		c.n--
		return false, errors.New("a responder is behind")
	}
	return c.held(kind, name, value), nil
}

func TestTheCALooksOnlyOnceEveryResponderHoldsTheAnswer(t *testing.T) {
	c := newCloud(t, nil)
	published := &answers{tokens: map[string]string{}}
	held := &confirmer{n: 3, held: func(kind, token, value string) bool {
		got, ok := published.answer(token)
		return kind == KindHTTP && ok && got == value
	}}
	i := c.issuer(t, certificate.ACME{}, t.TempDir(), WithHTTP(published), WithConfirm(held))
	var looked atomic.Int32
	c.answer = func(token string) (string, bool) {
		looked.Add(1)
		return published.answer(token)
	}
	cert, err := i.Certificate(t.Context(), "a.shop.example.io")
	require.NoError(t, err)
	c.verify(t, cert.Certificate[0], "a.shop.example.io")
	require.Len(t, held.asked, 4, "asked until every responder held it")
	require.NotZero(t, looked.Load())
	require.Empty(t, published.tokens, "the answer is withdrawn once the challenge is decided")

	// DNS-01 the same way.
	dnsHeld := &confirmer{n: 2, held: func(kind, fqdn, value string) bool {
		return kind == KindDNS && slices.Contains(c.txt.lookup(fqdn), value)
	}}
	d := c.issuer(t, certificate.ACME{}, t.TempDir(), WithDNS("tau.example.io", c.txt), WithConfirm(dnsHeld))
	cert, err = d.Certificate(t.Context(), "*.tau.example.io")
	require.NoError(t, err)
	c.verify(t, cert.Certificate[0], "x.tau.example.io")
	require.Len(t, dnsHeld.asked, 3)
}

func TestAnAnswerNotEveryResponderHoldsIsNeverShownToTheCA(t *testing.T) {
	c := newCloud(t, nil)
	published := &answers{tokens: map[string]string{}}
	never := &confirmer{held: func(string, string, string) bool { return false }}
	i := c.issuer(t, certificate.ACME{}, t.TempDir(), WithHTTP(published), WithConfirm(never), WithConfirmWait(300*time.Millisecond))
	var looked atomic.Int32
	c.answer = func(token string) (string, bool) {
		looked.Add(1)
		return published.answer(token)
	}
	_, err := i.Certificate(t.Context(), "a.shop.example.io")
	require.ErrorIs(t, err, ErrNotHeld)
	require.Zero(t, looked.Load(), "the CA was never asked to look")
	require.Empty(t, published.tokens)

	behind := &confirmer{n: 1 << 30}
	d := c.issuer(t, certificate.ACME{}, t.TempDir(), WithDNS("tau.example.io", c.txt), WithConfirm(behind), WithConfirmWait(300*time.Millisecond))
	_, err = d.Certificate(t.Context(), "*.tau.example.io")
	require.ErrorIs(t, err, ErrNotHeld)
	require.ErrorContains(t, err, "a responder is behind")
	require.Empty(t, c.txt.lookup("tau.example.io"))
}
