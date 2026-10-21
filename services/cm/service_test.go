package cm

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/letsencrypt/pebble/v2/ca"
	"github.com/letsencrypt/pebble/v2/db"
	"github.com/letsencrypt/pebble/v2/va"
	"github.com/letsencrypt/pebble/v2/wfe"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	mdns "github.com/miekg/dns"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
	pkgcache "github.com/taubyte/tau/pkg/cache"
	pkgcertificate "github.com/taubyte/tau/pkg/certificate"
	"github.com/taubyte/tau/pkg/certificate/acme"
	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
	cmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/cm/v2"
)

const zone = "tau.example.io"

// The CA is pebble, in process. Its resolver answers `_acme-challenge` TXT
// lookups from a copy of the challenge store, which is exactly what a dns
// node does, and A lookups outside the zone from loopback, where a server
// answers HTTP-01 for a custom domain's name.

type vault struct {
	mu   sync.Mutex
	data map[string][]byte
	fail error
}

func (v *vault) Get(_ context.Context, p, e, n string) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.fail != nil {
		return nil, v.fail
	}
	b, ok := v.data[p+"/"+e+"/"+n]
	if !ok {
		return nil, errors.New("not set")
	}
	return b, nil
}

func (v *vault) Set(_ context.Context, p, e, n string, value []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.fail != nil {
		return v.fail
	}
	v.data[p+"/"+e+"/"+n] = value
	return nil
}

func (v *vault) List(_ context.Context, p, e string) ([]string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.fail != nil {
		return nil, v.fail
	}
	var out []string
	for k := range v.data {
		if name, ok := strings.CutPrefix(k, p+"/"+e+"/"); ok {
			out = append(out, name)
		}
	}
	return out, nil
}

type fleet struct {
	mn      mocknet.Mocknet
	db      kvdb.KVDB
	vault   *vault
	cfg     Config
	chStore *pkgcache.MemStore
	nodes   []*Service
	ca      *ca.CAImpl
	pebble  *httptest.Server
	txt     *records
	http01  atomic.Pointer[func(string) (string, bool)]
	now     time.Time
	nowMu   sync.Mutex
	nodesMu sync.Mutex
}

func (f *fleet) clock() time.Time {
	f.nowMu.Lock()
	defer f.nowMu.Unlock()
	return f.now
}

func (f *fleet) advance(d time.Duration) {
	f.nowMu.Lock()
	f.now = f.now.Add(d)
	f.nowMu.Unlock()
}

func (f *fleet) members() []peer.ID {
	f.nodesMu.Lock()
	defer f.nodesMu.Unlock()
	var out []peer.ID
	for _, n := range f.nodes {
		out = append(out, n.host.ID())
	}
	return out
}

func newHost(t *testing.T, mn mocknet.Mocknet) host.Host {
	t.Helper()
	h, err := mn.GenPeer()
	require.NoError(t, err)
	return h
}

func (f *fleet) challenges(t *testing.T) *Challenges {
	t.Helper()
	c, err := ChallengesIn(t.Context(), f.chStore, f.clock)
	require.NoError(t, err)
	return c
}

// newFleet starts pebble and n cm nodes over one database and one
// vault, which is what replication converges them to.
func newFleet(t *testing.T, n int, wildcard bool) *fleet {
	t.Helper()
	t.Setenv("PEBBLE_VA_NOSLEEP", "1")
	t.Setenv("PEBBLE_WFE_NONCEREJECT", "0")
	t.Setenv("PEBBLE_AUTHZREUSE", "0")
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	database, err := kvdbmock.New().New(nil, "certificate", 0)
	require.NoError(t, err)
	f := &fleet{mn: mn, db: database, vault: &vault{data: map[string][]byte{VaultProject + "/" + VaultEnvironment + "/" + AccountSecret: []byte(accountKey(t))}}, now: time.Now(), chStore: pkgcache.NewMemStore(), txt: &records{m: map[string][]string{}}}

	logger := log.New(io.Discard, "", 0)
	store := db.NewMemoryStore()
	f.ca = ca.New(logger, store, "", "ecdsa", 0, 1, map[string]ca.Profile{"default": {Description: "default"}})

	reader := f.challenges(t)
	reader.wait = 0
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &mdns.Server{Listener: l, Handler: mdns.HandlerFunc(func(w mdns.ResponseWriter, r *mdns.Msg) {
		m := new(mdns.Msg)
		m.SetReply(r)
		q := r.Question[0]
		name := strings.TrimSuffix(q.Name, ".")
		if q.Qtype == mdns.TypeA && !under(zone, name) {
			m.Answer = append(m.Answer, &mdns.A{Hdr: mdns.RR_Header{Name: q.Name, Rrtype: mdns.TypeA, Class: mdns.ClassINET}, A: net.IPv4(127, 0, 0, 1)})
		}
		if values, err := f.txt.LookupTXT(context.Background(), name); err == nil && q.Qtype == mdns.TypeTXT {
			for _, v := range values {
				m.Answer = append(m.Answer, &mdns.TXT{Hdr: mdns.RR_Header{Name: q.Name, Rrtype: mdns.TypeTXT, Class: mdns.ClassINET}, Txt: []string{v}})
			}
		}
		if fqdn, ok := strings.CutPrefix(strings.TrimSuffix(q.Name, "."), "_acme-challenge."); ok && q.Qtype == mdns.TypeTXT {
			values, _ := reader.Challenges(context.Background(), fqdn)
			for _, v := range values {
				m.Answer = append(m.Answer, &mdns.TXT{Hdr: mdns.RR_Header{Name: q.Name, Rrtype: mdns.TypeTXT, Class: mdns.ClassINET, Ttl: 0}, Txt: []string{v}})
			}
		}
		w.WriteMsg(m)
	})}
	go srv.ActivateAndServe()
	t.Cleanup(func() { srv.Shutdown() })

	challenge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		answer := f.http01.Load()
		token, ok := strings.CutPrefix(r.URL.Path, "/.well-known/acme-challenge/")
		if answer == nil || !ok {
			http.NotFound(w, r)
			return
		}
		if a, ok := (*answer)(token); ok {
			io.WriteString(w, a)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(challenge.Close)
	v := va.New(logger, challenge.Listener.Addr().(*net.TCPAddr).Port, 0, false, l.Addr().String(), store)
	front := wfe.New(logger, store, v, f.ca, nil, false, false, 0, 0)
	f.pebble = httptest.NewTLSServer(front.Handler())
	t.Cleanup(f.pebble.Close)

	f.cfg = Config{Certificate: CertificateConfig{
		ACME: pkgcertificate.ACME{
			Enabled:   true,
			Directory: f.pebble.URL + "/dir",
			RootCA:    string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.pebble.Certificate().Raw})),
		},
		Wildcard: wildcard,
		Zone:     zone + ".",
	}}
	for range n {
		f.add(t)
	}
	f.connect(t)
	return f
}

func (f *fleet) add(t *testing.T) *Service {
	t.Helper()
	s, err := New(newHost(t, f.mn), f.db, f.cfg, WithClock(f.clock), WithVault(f.vault), WithResolver(f.txt))
	require.NoError(t, err)
	require.NoError(t, s.Start(t.Context()))
	t.Cleanup(func() { s.Close() })
	f.nodesMu.Lock()
	f.nodes = append(f.nodes, s)
	f.nodesMu.Unlock()
	return s
}

// client is a node that is not a cm node, with its own copy of the
// challenge store.
func (f *fleet) client(t *testing.T, opts ...ClientOption) *Client {
	t.Helper()
	c, err := NewClient(newHost(t, f.mn), f.members, func() *Service { return nil }, f.vault, f.cfg, append([]ClientOption{
		WithChallengeStore(f.challenges(t)), WithClientClock(f.clock), WithCertWait(0), WithLogger(t.Logf),
	}, opts...)...)
	require.NoError(t, err)
	c.poll = 20 * time.Millisecond
	t.Cleanup(func() { c.Close() })
	f.connect(t)
	return c
}

func (f *fleet) connect(t *testing.T) {
	t.Helper()
	require.NoError(t, f.mn.LinkAll())
	require.NoError(t, f.mn.ConnectAllButSelf())
}

func (f *fleet) verify(t *testing.T, cert tls.Certificate, name string) {
	t.Helper()
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(f.ca.GetRootCert(0).PEM())
	inter := x509.NewCertPool()
	inter.AppendCertsFromPEM(f.ca.GetIntermediateCert(0).PEM())
	_, err := cert.Leaf.Verify(x509.VerifyOptions{DNSName: name, Roots: roots, Intermediates: inter})
	require.NoError(t, err)
}

func (f *fleet) kept(t *testing.T, name string) int {
	t.Helper()
	keys, err := f.db.List(t.Context(), certPrefix+name+"/")
	require.NoError(t, err)
	return len(keys)
}

func TestANodeOrdersWhatItNeedsAndAnotherReadsIt(t *testing.T) {
	f := newFleet(t, 2, false)
	c := f.client(t)

	for _, name := range []string{"build-frontend.tau.example.io", "api.shop.tau.example.io"} {
		cert, err := c.GetCertificate(&tls.ClientHelloInfo{ServerName: strings.ToUpper(name) + "."})
		require.NoError(t, err, name)
		f.verify(t, *cert, name)

		// ANOTHER NODE READS THE SAME ONE, from the store and the vault.
		again, err := f.client(t).Certificate(t.Context(), name)
		require.NoError(t, err)
		require.Equal(t, cert.Certificate[0], again.Certificate[0])
		require.Equal(t, 1, f.kept(t, name))
	}

	// ONE ACCOUNT, and HTTP-01 answered by any node from it.
	a, ok := c.Challenge("tok")
	require.True(t, ok)
	b, ok := f.client(t).Challenge("tok")
	require.True(t, ok)
	require.Equal(t, a, b)
	require.True(t, strings.HasPrefix(a, "tok."))
	_, ok = c.Challenge("not/a/token")
	require.False(t, ok)

	// NO TXT RECORD OUTLIVES ITS CHALLENGE.
	values, err := f.challenges(t).values(t.Context(), "build-frontend.tau.example.io")
	require.NoError(t, err)
	require.Empty(t, values)

	// A NAME OUTSIDE THE ZONE is refused before any node is asked.
	_, err = c.Certificate(t.Context(), "shop.example.com")
	require.ErrorIs(t, err, ErrNotZone)
	_, err = c.GetCertificate(&tls.ClientHelloInfo{})
	require.Error(t, err)
}

func TestTheWildcardAnswersEveryServiceName(t *testing.T) {
	f := newFleet(t, 1, true)
	c := f.client(t)

	cert, err := c.Certificate(t.Context(), "dns.tau.example.io")
	require.NoError(t, err)
	require.Equal(t, []string{"*.tau.example.io"}, cert.Leaf.DNSNames)
	f.verify(t, cert, "dns.tau.example.io")

	other, err := c.Certificate(t.Context(), "build-frontend.tau.example.io")
	require.NoError(t, err)
	require.Equal(t, cert.Certificate[0], other.Certificate[0], "the one wildcard, not a second issuance")

	generated, err := c.Certificate(t.Context(), "api.shop.tau.example.io")
	require.NoError(t, err)
	require.Equal(t, []string{"api.shop.tau.example.io"}, generated.Leaf.DNSNames)
}

func TestANodeWaitsForOneOrderedElsewhere(t *testing.T) {
	f := newFleet(t, 1, false)
	const name = "w." + zone
	waiting := f.client(t, WithCertWait(time.Minute))
	got := make(chan tls.Certificate, 1)
	go func() {
		cert, err := waiting.Certificate(context.Background(), name)
		if err == nil {
			got <- cert
		}
		close(got)
	}()
	ordered, err := f.client(t).Certificate(t.Context(), name)
	require.NoError(t, err)
	select {
	case cert := <-got:
		require.Equal(t, ordered.Certificate[0], cert.Certificate[0])
	case <-time.After(30 * time.Second):
		t.Fatal("the waiting node never read the one ordered elsewhere")
	}
	require.Equal(t, 1, f.kept(t, name), "the waiting node ordered nothing")
}

func TestTwoOrdersOfOneNameConvergeOnTheNewest(t *testing.T) {
	f := newFleet(t, 2, false)
	const name = "dup." + zone
	a, b := f.client(t), f.client(t)
	first, err := a.Certificate(t.Context(), name)
	require.NoError(t, err)
	// b ORDERS ITS OWN as a node that did not see a's would.
	issuer, err := b.acmeIssuer(t.Context())
	require.NoError(t, err)
	_, err = issuer.Issue(name)
	require.NoError(t, err)
	require.Equal(t, 2, f.kept(t, name))
	newest, err := f.nodes[0].newest(t.Context(), name)
	require.NoError(t, err)

	got, err := b.Certificate(t.Context(), name)
	require.NoError(t, err)
	require.Equal(t, newest.leaf.Raw, got.Certificate[0])

	// a SERVES ITS OWN until it reads the store again, then the newest.
	again, err := a.Certificate(t.Context(), name)
	require.NoError(t, err)
	require.Equal(t, first.Certificate[0], again.Certificate[0])
	f.advance(Recheck)
	require.Eventually(t, func() bool {
		got, err := a.Certificate(t.Context(), name)
		return err == nil && slices.Equal(got.Certificate[0], newest.leaf.Raw)
	}, 10*time.Second, 20*time.Millisecond)
}

func TestADueCertificateIsRenewedAndAnOutageKeepsTheOldOne(t *testing.T) {
	f := newFleet(t, 1, false)
	s := f.nodes[0]
	// A CLIENT ON THE GLOBALS NODE ITSELF calls it without a stream.
	c, err := NewClient(s.host, func() []peer.ID { return nil }, func() *Service { return s }, f.vault, f.cfg,
		WithChallengeStore(f.challenges(t)), WithClientClock(f.clock), WithCertWait(0))
	require.NoError(t, err)
	const name = "r." + zone

	first, err := c.TLSConfig().GetCertificate(&tls.ClientHelloInfo{ServerName: name})
	require.NoError(t, err)

	// DUE: the cached one answers while its successor is ordered.
	f.advance(first.Leaf.NotAfter.Sub(first.Leaf.NotBefore) * 3 / 4)
	again, err := c.Certificate(t.Context(), name)
	require.NoError(t, err)
	require.Equal(t, first.Certificate[0], again.Certificate[0])
	require.Eventually(t, func() bool {
		got, err := c.Certificate(t.Context(), name)
		return err == nil && !got.Leaf.Equal(first.Leaf)
	}, 30*time.Second, 50*time.Millisecond, "the renewed certificate replaces it")

	// THE CA GONE, a due certificate is still served. pebble dates what it
	// issues by the real clock, so the renewed one is due by the advanced
	// clock already.
	f.pebble.Close()
	renewed, err := s.newest(t.Context(), name)
	require.NoError(t, err)
	got, err := c.obtain(t.Context(), name)
	require.NoError(t, err)
	require.Equal(t, renewed.leaf.Raw, got.Certificate[0])

	// AND A NAME NOTHING IS KEPT FOR is an error, not a wrong certificate.
	_, err = c.Certificate(t.Context(), "never."+zone)
	require.Error(t, err)
}

func TestWhatIsRefused(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)
	database, err := kvdbmock.New().New(nil, "c", 0)
	require.NoError(t, err)
	cfg := func(c CertificateConfig) Config { return Config{Certificate: c} }
	acmeOn := pkgcertificate.ACME{Enabled: true}
	on := cfg(CertificateConfig{ACME: acmeOn, Zone: zone})

	_, err = New(nil, database, on)
	require.ErrorIs(t, err, ErrNoHost)
	_, err = New(h, nil, on)
	require.ErrorIs(t, err, ErrNoDB)
	_, err = New(h, database, cfg(CertificateConfig{Zone: zone}))
	require.NoError(t, err, "a cloud that issues nothing still proves its domains")
	_, err = New(h, database, cfg(CertificateConfig{}))
	require.ErrorIs(t, err, ErrNoZone)
	_, err = New(h, database, cfg(CertificateConfig{ACME: acmeOn}))
	require.ErrorIs(t, err, ErrNoZone)
	_, err = New(h, database, cfg(CertificateConfig{ACME: acmeOn, Zone: zone, Challenge: "tls-alpn-01"}))
	require.ErrorContains(t, err, "neither")
	_, err = New(h, database, cfg(CertificateConfig{ACME: acmeOn, Zone: zone, Challenge: ChallengeHTTP, Wildcard: true}))
	require.ErrorContains(t, err, "dns-01 only")
	_, err = New(h, database, cfg(CertificateConfig{ACME: acmeOn, Zone: zone, Challenge: ChallengeDNS}))
	require.NoError(t, err)
	s, err := New(h, database, on)
	require.NoError(t, err)
	require.NoError(t, s.Close(), "closing what was never started")

	empty := &vault{data: map[string][]byte{}}
	none := func() []peer.ID { return nil }
	nolocal := func() *Service { return nil }
	_, err = NewClient(nil, none, nolocal, empty, on)
	require.ErrorIs(t, err, ErrNoHost)
	_, err = NewClient(h, none, nolocal, nil, on)
	require.ErrorIs(t, err, ErrNoVault)
	_, err = NewClient(h, none, nolocal, empty, cfg(CertificateConfig{ACME: acmeOn}))
	require.ErrorIs(t, err, ErrNoZone)

	// NO KEY IN THE VAULT: nothing is issued, and no challenge answered.
	c, err := NewClient(h, none, nolocal, empty, on)
	require.NoError(t, err)
	c.timeout, c.poll = 20*time.Millisecond, time.Millisecond
	_, ok := c.Challenge("tok")
	require.False(t, ok)
	short, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err = c.acmeIssuer(short)
	require.ErrorIs(t, err, ErrNoAccount)
	empty.data[VaultProject+"/"+VaultEnvironment+"/"+AccountSecret] = []byte("not pem")
	_, err = c.acmeIssuer(t.Context())
	require.Error(t, err)

	// THE VAULT'S KEY is the account; DNS-01 without a store is refused.
	key := accountKey(t)
	empty.data[VaultProject+"/"+VaultEnvironment+"/"+AccountSecret] = []byte(key)
	_, ok = c.Challenge("tok")
	require.True(t, ok)
	want, err := acme.ParseKey([]byte(key))
	require.NoError(t, err)
	require.True(t, c.account.Public().(interface{ Equal(crypto.PublicKey) bool }).Equal(want.Public()))
	_, err = c.acmeIssuer(t.Context())
	require.ErrorIs(t, err, ErrNoChallenges)

	// A REACHABLE CLIENT REACHING NO MEMBER says so.
	_, err = c.Certificate(t.Context(), "a."+zone)
	require.ErrorIs(t, err, ErrNoMembers)
	require.ErrorIs(t, keeper{c}.Keep(t.Context(), "a."+zone, pemOf(t)), ErrNoMembers)
	require.ErrorContains(t, keeper{c}.Keep(t.Context(), "a."+zone, []byte("no pem")), "no chain")

	// HTTP-01 needs no store.
	httpOn := cfg(CertificateConfig{ACME: acmeOn, Zone: zone, Challenge: ChallengeHTTP})
	c, err = NewClient(h, none, nolocal, empty, httpOn)
	require.NoError(t, err)
	_, err = c.acmeIssuer(t.Context())
	require.NoError(t, err)

	// A VAULT THAT FAILS fails a leaf key.
	broken := &vault{data: map[string][]byte{}, fail: errors.New("down")}
	c, err = NewClient(h, none, nolocal, broken, httpOn)
	require.NoError(t, err)
	require.ErrorContains(t, keeper{c}.Keep(t.Context(), "a."+zone, pemOf(t)), "down")
}

// accountKey is a fresh ACME account key, PEM.
func accountKey(t *testing.T) string {
	t.Helper()
	_, blob, err := acme.MintKey()
	require.NoError(t, err)
	return string(blob)
}

// pemOf is a self-signed chain and its key, as an issuer hands a store.
func pemOf(t *testing.T) []byte {
	t.Helper()
	key, keyPEM, err := acme.MintKey()
	require.NoError(t, err)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"a." + zone}, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	require.NoError(t, err)
	return append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), keyPEM...)
}

func TestTheStoreKeepsOnlyWhatItCanServe(t *testing.T) {
	f := newFleet(t, 1, false)
	s := f.nodes[0]
	h := handler{s}
	const name = "a." + zone
	far := f.clock().Add(time.Hour)
	require.NoError(t, f.db.Put(t.Context(), certKey(name, far, "a"), []byte("not a record")))
	_, err := s.newest(t.Context(), name)
	require.ErrorIs(t, err, ErrNotKept)
	_, err = h.Certificate(t.Context(), connect.NewRequest(&cmv2.CertificateRequest{Name: name}))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	blob := pemOf(t)
	block, _ := pem.Decode(blob)
	chain := pem.EncodeToMemory(block)
	ref := &cmv2.SecretRef{Project: VaultProject, Environment: VaultEnvironment, Name: "leaf-x"}
	keep := func(rec *cmv2.Issued) error {
		_, err := h.Keep(t.Context(), connect.NewRequest(rec))
		return err
	}
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(keep(&cmv2.Issued{Name: "a.example.com", Chain: chain, Key: ref})))
	require.Error(t, keep(&cmv2.Issued{Name: name, Chain: []byte("x"), Key: ref}))
	require.ErrorContains(t, keep(&cmv2.Issued{Name: "b." + zone, Chain: chain, Key: ref}), "does not name it")
	require.ErrorContains(t, keep(&cmv2.Issued{Name: name, Chain: chain, Key: &cmv2.SecretRef{Project: "shop"}}), "certificate secrets")
	require.NoError(t, keep(&cmv2.Issued{Name: name, Chain: chain, Key: ref}))
	got, err := h.Certificate(t.Context(), connect.NewRequest(&cmv2.CertificateRequest{Name: name}))
	require.NoError(t, err)
	require.Equal(t, "leaf-x", got.Msg.GetKey().GetName())

	// A KEY THE VAULT DOES NOT HOLD fails the read by name.
	c := f.client(t)
	_, err = c.Certificate(t.Context(), name)
	require.ErrorContains(t, err, "opening the key")
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(wire(errors.New("x"))))
}

func TestChallengesAreOnePerPresentationAndWaitedFor(t *testing.T) {
	f := newFleet(t, 0, false)
	a, b := f.challenges(t), f.challenges(t)
	a.wait = 2 * time.Second
	const name = "c." + zone
	require.NoError(t, a.Present(t.Context(), name, "v"))
	require.NoError(t, b.Present(t.Context(), name, "v"))
	values, err := a.Challenges(t.Context(), name)
	require.NoError(t, err)
	require.Equal(t, []string{"v", "v"}, values, "two orders presenting one value hold two entries")
	require.NoError(t, b.CleanUp(t.Context(), name, "v"))
	require.NoError(t, b.CleanUp(t.Context(), name, "v"), "withdrawing twice is nothing")
	values, err = a.Challenges(t.Context(), name)
	require.NoError(t, err)
	require.Equal(t, []string{"v"}, values, "b withdrew only its own")

	// EXPIRED, nothing answers, after the wait.
	f.advance(ChallengeTTL + time.Second)
	a.wait = 50 * time.Millisecond
	_, err = a.Challenges(t.Context(), name)
	require.ErrorIs(t, err, ErrNoValue)

	// A VALUE ARRIVING DURING THE WAIT is answered.
	a.wait = 5 * time.Second
	go func() {
		time.Sleep(200 * time.Millisecond)
		b.Present(context.Background(), "late."+zone, "w")
	}()
	values, err = a.Challenges(t.Context(), "late."+zone)
	require.NoError(t, err)
	require.Equal(t, []string{"w"}, values)

	_, err = ChallengesIn(t.Context(), nil, f.clock)
	require.ErrorIs(t, err, ErrNoChallenges)
}

// TestAKeyNotReadableYetIsWaitedFor: a CA reaching a node whose vault has not
// caught up is still answered.
func TestAKeyNotReadableYetIsWaitedFor(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	v := &vault{data: map[string][]byte{}}
	c, err := NewClient(newHost(t, mn), func() []peer.ID { return nil }, func() *Service { return nil }, v,
		Config{Certificate: CertificateConfig{ACME: pkgcertificate.ACME{Enabled: true}, Zone: zone}})
	require.NoError(t, err)
	c.poll = time.Millisecond
	key := accountKey(t)
	go func() {
		time.Sleep(30 * time.Millisecond)
		v.Set(t.Context(), VaultProject, VaultEnvironment, AccountSecret, []byte(key))
	}()
	_, ok := c.Challenge("tok")
	require.True(t, ok)
}
