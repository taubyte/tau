package certificate

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
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
	pkgcertificate "github.com/taubyte/tau/pkg/certificate"
	"github.com/taubyte/tau/pkg/certificate/acme"
	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
	certificatev2 "github.com/taubyte/tau/pkg/proto/gen/tau/certificate/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/certificate/v2/certificatev2connect"
)

const zone = "tau.example.io"

// The CA is pebble, in process. Its resolver answers `_acme-challenge` TXT
// lookups by asking the certificate nodes through a Client, which is exactly
// what a dns node does; it has no HTTP-01 port to reach, so every name here
// is proved by DNS-01.

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
	nodes   []*Service
	ca      *ca.CAImpl
	pebble  *httptest.Server
	clients []*Client
	now     time.Time
	nowMu   sync.Mutex
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

// newFleet starts pebble and n certificate nodes over one database and one
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
	f := &fleet{mn: mn, db: database, vault: &vault{data: map[string][]byte{}}, now: time.Now()}

	logger := log.New(io.Discard, "", 0)
	store := db.NewMemoryStore()
	f.ca = ca.New(logger, store, "", "ecdsa", 0, 1, map[string]ca.Profile{"default": {Description: "default"}})

	// The resolver asks a client on a host of its own.
	asker := f.client(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &mdns.Server{Listener: l, Handler: mdns.HandlerFunc(func(w mdns.ResponseWriter, r *mdns.Msg) {
		m := new(mdns.Msg)
		m.SetReply(r)
		q := r.Question[0]
		if fqdn, ok := strings.CutPrefix(strings.TrimSuffix(q.Name, "."), "_acme-challenge."); ok && q.Qtype == mdns.TypeTXT {
			values, _ := asker.Challenges(context.Background(), fqdn)
			for _, v := range values {
				m.Answer = append(m.Answer, &mdns.TXT{Hdr: mdns.RR_Header{Name: q.Name, Rrtype: mdns.TypeTXT, Class: mdns.ClassINET, Ttl: 1}, Txt: []string{v}})
			}
		}
		w.WriteMsg(m)
	})}
	go srv.ActivateAndServe()
	t.Cleanup(func() { srv.Shutdown() })

	v := va.New(logger, 1, 0, false, l.Addr().String(), store)
	front := wfe.New(logger, store, v, f.ca, nil, false, false, 0, 0)
	f.pebble = httptest.NewTLSServer(front.Handler())
	t.Cleanup(f.pebble.Close)

	f.cfg = Config{
		ACME: pkgcertificate.ACME{
			Enabled:   true,
			Directory: f.pebble.URL + "/dir",
			RootCA:    string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.pebble.Certificate().Raw})),
		},
		Wildcard:   wildcard,
		Zone:       zone + ".",
		RenewEvery: time.Hour,
	}
	for range n {
		f.add(t)
	}
	require.NoError(t, mn.LinkAll())
	require.NoError(t, mn.ConnectAllButSelf())
	return f
}

func (f *fleet) add(t *testing.T, opts ...Option) *Service {
	t.Helper()
	h := newHost(t, f.mn)
	s, err := New(h, f.db, f.cfg, append([]Option{
		WithVault(f.vault), WithPeers(f.members), WithClock(f.clock),
		WithLogger(t.Logf),
	}, opts...)...)
	require.NoError(t, err)
	require.NoError(t, s.Start(t.Context()))
	t.Cleanup(func() { s.Close() })
	f.nodes = append(f.nodes, s)
	return s
}

func (f *fleet) client(t *testing.T) *Client {
	t.Helper()
	c, err := NewClient(newHost(t, f.mn), f.members, func() *Service { return nil }, f.vault, zone)
	require.NoError(t, err)
	c.now = f.clock
	t.Cleanup(func() { c.Close() })
	f.clients = append(f.clients, c)
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

func TestAServiceNameAndAGeneratedNameAreIssuedByDNS01AndShared(t *testing.T) {
	f := newFleet(t, 2, false)
	c := f.client(t)
	f.connect(t)

	for _, name := range []string{"build-frontend.tau.example.io", "api.shop.tau.example.io"} {
		cert, err := c.GetCertificate(&tls.ClientHelloInfo{ServerName: strings.ToUpper(name) + "."})
		require.NoError(t, err, name)
		f.verify(t, *cert, name)

		// ANOTHER NODE READS THE SAME ONE, from the store and the vault.
		other := f.client(t)
		f.connect(t)
		again, err := other.Certificate(t.Context(), name)
		require.NoError(t, err)
		require.Equal(t, cert.Certificate[0], again.Certificate[0], "one issuance per name, cluster-wide")
	}
	keys, err := f.db.List(t.Context(), certPrefix)
	require.NoError(t, err)
	require.Len(t, keys, 2)

	// EVERY CERTIFICATE NODE ADOPTED ONE ACCOUNT KEY.
	a, err := f.nodes[0].Thumbprint(t.Context())
	require.NoError(t, err)
	b, err := f.nodes[1].Thumbprint(t.Context())
	require.NoError(t, err)
	require.Equal(t, a, b)
	answer, ok := c.Challenge("tok")
	require.True(t, ok)
	require.Equal(t, "tok."+a, answer)

	// NO TXT RECORD OUTLIVES ITS CHALLENGE.
	values, err := c.Challenges(t.Context(), "build-frontend.tau.example.io")
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
	f.connect(t)

	cert, err := c.Certificate(t.Context(), "dns.tau.example.io")
	require.NoError(t, err)
	require.Equal(t, []string{"*.tau.example.io"}, cert.Leaf.DNSNames)
	f.verify(t, cert, "dns.tau.example.io")

	other, err := c.Certificate(t.Context(), "build-frontend.tau.example.io")
	require.NoError(t, err)
	require.Equal(t, cert.Certificate[0], other.Certificate[0], "the one wildcard, not a second issuance")

	// A GENERATED NAME IS TWO LABELS UNDER THE ZONE, which the wildcard does
	// not cover, so it is issued its own.
	generated, err := c.Certificate(t.Context(), "api.shop.tau.example.io")
	require.NoError(t, err)
	require.Equal(t, []string{"api.shop.tau.example.io"}, generated.Leaf.DNSNames)
}

func TestTheRenewalLoopIssuesWhatItOwnsAndRenewsWhatIsDue(t *testing.T) {
	f := newFleet(t, 0, true)
	s := f.add(t, WithNames(func() []string { return []string{"build-frontend." + zone, "outside.example.com"} }))
	f.connect(t)

	s.renewPass(t.Context())
	names, err := s.keptNames(t.Context())
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"*." + zone}, names, "the service name is the wildcard's; a name outside the zone is never wanted")

	first, err := s.newest(t.Context(), "*."+zone)
	require.NoError(t, err)

	// DUE: two thirds of its life gone.
	f.advance(first.leaf.NotAfter.Sub(first.leaf.NotBefore) * 3 / 4)
	s.renewPass(t.Context())
	renewed, err := s.newest(t.Context(), "*."+zone)
	require.NoError(t, err)
	require.NotEqual(t, first.leaf.SerialNumber, renewed.leaf.SerialNumber)
	keys, err := f.db.List(t.Context(), certPrefix)
	require.NoError(t, err)
	require.Len(t, keys, 2, "both issuances are kept; the newest is read")
}

func TestOneLeaseWinsAndAnExpiredOneDoesNot(t *testing.T) {
	f := newFleet(t, 2, false)
	a, b := f.nodes[0], f.nodes[1]
	const name = "x." + zone

	won, err := a.claim(t.Context(), name)
	require.NoError(t, err)
	require.True(t, won)
	f.advance(time.Millisecond)
	won, err = b.claim(t.Context(), name)
	require.NoError(t, err)
	require.False(t, won, "the earlier lease holds")

	// A LOSER WITH NOTHING KEPT says who holds it rather than ordering.
	_, err = b.Ensure(t.Context(), name)
	require.ErrorIs(t, err, ErrIssuing)

	// EXPIRED, the earlier lease no longer counts.
	f.advance(DefaultLease + time.Second)
	won, err = b.claim(t.Context(), name)
	require.NoError(t, err)
	require.True(t, won)
	b.release(name)

	// A malformed lease is ignored.
	require.NoError(t, f.db.Put(t.Context(), leasePrefix+name+"/junk", []byte("x")))
	won, err = b.claim(t.Context(), name)
	require.NoError(t, err)
	require.True(t, won)
}

func TestOwnerIsDeterministic(t *testing.T) {
	peers := []peer.ID{"a", "b", "c"}
	first, ok := Owner("n", peers)
	require.True(t, ok)
	reversed := []peer.ID{"c", "b", "a"}
	again, _ := Owner("n", reversed)
	require.Equal(t, first, again)
	_, ok = Owner("n", nil)
	require.False(t, ok)
}

func TestWhatIsRefused(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)
	database, err := kvdbmock.New().New(nil, "c", 0)
	require.NoError(t, err)
	on := Config{ACME: pkgcertificate.ACME{Enabled: true}, Zone: zone}

	_, err = New(nil, database, on)
	require.ErrorIs(t, err, ErrNoHost)
	_, err = New(h, nil, on)
	require.ErrorIs(t, err, ErrNoDB)
	_, err = New(h, database, Config{Zone: zone})
	require.ErrorIs(t, err, ErrDisabled)
	_, err = New(h, database, Config{ACME: on.ACME})
	require.ErrorIs(t, err, ErrNoZone)

	s, err := New(h, database, on)
	require.NoError(t, err)
	_, err = s.Thumbprint(t.Context())
	require.ErrorIs(t, err, ErrNoVault, "no vault and no configured account key")
	require.ErrorIs(t, store{s}.Keep(t.Context(), "n", nil), ErrNoVault)
	_, err = s.Ensure(t.Context(), "elsewhere.example.com")
	require.ErrorIs(t, err, ErrNotZone)
	require.NoError(t, s.Close(), "closing what was never started")

	// A CONFIGURED ACCOUNT KEY is used as it is, with no vault.
	_, blob, err := acme.MintKey()
	require.NoError(t, err)
	configured := on
	configured.ACME.AccountKey = string(blob)
	s, err = New(h, database, configured)
	require.NoError(t, err)
	thumb, err := s.Thumbprint(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, thumb)

	// A VAULT THAT FAILS fails the account key, and the keeping of a key.
	broken := &vault{data: map[string][]byte{}, fail: errors.New("down")}
	s, err = New(h, database, on, WithVault(broken))
	require.NoError(t, err)
	_, err = s.Thumbprint(t.Context())
	require.ErrorContains(t, err, "down")

	_, err = NewClient(nil, nil, nil, broken, zone)
	require.ErrorIs(t, err, ErrNoHost)
	_, err = NewClient(h, nil, nil, nil, zone)
	require.ErrorIs(t, err, ErrNoVault)

	// A CLIENT REACHING NO MEMBER says so.
	c, err := NewClient(h, func() []peer.ID { return nil }, func() *Service { return nil }, broken, zone)
	require.NoError(t, err)
	_, err = c.Certificate(t.Context(), "a."+zone)
	require.ErrorIs(t, err, ErrNoMembers)
	_, ok := c.Challenge("tok")
	require.False(t, ok)
	_, err = c.Challenges(t.Context(), "a."+zone)
	require.ErrorIs(t, err, ErrNoMembers)
}

func TestKeptRecordsThatCannotBeReadAreSkipped(t *testing.T) {
	f := newFleet(t, 1, false)
	s := f.nodes[0]
	const name = "k." + zone
	far := f.clock().Add(time.Hour)
	require.NoError(t, f.db.Put(t.Context(), certKey(name, far, "a"), []byte("not a record")))
	_, err := s.newest(t.Context(), name)
	require.ErrorIs(t, err, ErrNotKept)
	_, err = store{s}.Load(t.Context(), name)
	require.ErrorIs(t, err, acme.ErrMiss)
	require.Error(t, store{s}.Keep(t.Context(), name, []byte("no pem")))

	// AN EXPIRED CHALLENGE is not answered.
	require.NoError(t, presenter{s}.Present(t.Context(), name, "v"))
	values, err := s.Challenges(t.Context(), name)
	require.NoError(t, err)
	require.Equal(t, []string{"v"}, values)
	f.advance(DefaultLease + time.Second)
	values, err = s.Challenges(t.Context(), name)
	require.NoError(t, err)
	require.Empty(t, values)
}

func TestADueCertificateIsRefetchedAndAnOutageKeepsTheOldOne(t *testing.T) {
	f := newFleet(t, 1, false)
	s := f.nodes[0]
	// A CLIENT ON THE CERTIFICATE NODE ITSELF calls it without a stream.
	c, err := NewClient(s.host, func() []peer.ID { return nil }, func() *Service { return s }, f.vault, zone)
	require.NoError(t, err)
	c.now = f.clock
	const name = "r." + zone

	first, err := c.TLSConfig().GetCertificate(&tls.ClientHelloInfo{ServerName: name})
	require.NoError(t, err)
	blob, err := store{s}.Load(t.Context(), name)
	require.NoError(t, err)
	require.Contains(t, string(blob), "PRIVATE KEY")

	// DUE: the cached one answers while its successor is fetched.
	f.advance(first.Leaf.NotAfter.Sub(first.Leaf.NotBefore) * 3 / 4)
	again, err := c.Certificate(t.Context(), name)
	require.NoError(t, err)
	require.Equal(t, first.Certificate[0], again.Certificate[0])
	require.Eventually(t, func() bool {
		got, err := c.Certificate(t.Context(), name)
		return err == nil && !got.Leaf.Equal(first.Leaf)
	}, 30*time.Second, 50*time.Millisecond, "the renewed certificate replaces it")

	// THE CA GONE, a due certificate is still served by the node. pebble
	// dates what it issues by the real clock, so the renewed one is due by
	// the advanced clock already.
	f.pebble.Close()
	renewed, err := s.newest(t.Context(), name)
	require.NoError(t, err)
	require.True(t, s.due(renewed))
	kept, err := s.Ensure(t.Context(), name)
	require.NoError(t, err)
	require.Equal(t, renewed.leaf.SerialNumber, kept.leaf.SerialNumber)

	// AND A NAME NOTHING IS KEPT FOR is an error, not a wrong certificate.
	_, err = s.Ensure(t.Context(), "never."+zone)
	require.Error(t, err)
}

func TestTheWireSaysWhatWentWrong(t *testing.T) {
	f := newFleet(t, 1, false)
	h := handler{f.nodes[0]}

	_, err := h.Certificate(t.Context(), connectRequest("elsewhere.example.com", true))
	require.Equal(t, "invalid_argument", codeOf(err))
	_, err = h.Certificate(t.Context(), connectRequest("absent."+zone, false))
	require.Equal(t, "not_found", codeOf(err))

	f.vault.mu.Lock()
	f.vault.fail = errors.New("down")
	f.vault.mu.Unlock()
	_, err = h.Thumbprint(t.Context(), connect.NewRequest(&certificatev2.Empty{}))
	require.Equal(t, "unavailable", codeOf(err))
	_, err = h.Certificate(t.Context(), connectRequest("down."+zone, true))
	require.Equal(t, "unavailable", codeOf(err))

	// A CLIENT whose every target refuses the name stops at the first.
	c := f.client(t)
	f.connect(t)
	err = c.each("x", func(svc certificatev2connect.CertificateServiceClient) error {
		_, err := svc.Certificate(t.Context(), connectRequest("elsewhere.example.com", true))
		return err
	})
	require.ErrorContains(t, err, "only names in the cloud's zone")

	// A KEY THE VAULT CANNOT OPEN fails the fetch by name.
	_, err = c.Certificate(t.Context(), "down."+zone)
	require.Error(t, err)
}

func connectRequest(name string, issue bool) *connect.Request[certificatev2.CertificateRequest] {
	return connect.NewRequest(&certificatev2.CertificateRequest{Name: name, Issue: issue})
}

func codeOf(err error) string {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce.Code().String()
	}
	return ""
}
