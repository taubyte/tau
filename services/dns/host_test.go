package dns

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/peer/peertest"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/certificate"
	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
	"github.com/taubyte/tau/services/cm"
	statsService "github.com/taubyte/tau/services/stats"
)

// node is what dns starts on: a peer, what runs beside it, and its section.
type node struct {
	peer.Node
	*service.Served
	running []service.Running
	config  func(*hostConfig) error
	// cm is the cloud-wide `cm:` block every section on a node carries.
	cm cm.Config
}

func (n *node) Peer() peer.Node            { return n.Node }
func (n *node) Running() []service.Running { return n.running }
func (*node) Root() string                 { return "" }
func (*node) Cloud() string                { return "dns-test" }
func (*node) Region() string               { return "" }
func (*node) Now() time.Time               { return time.Now() }

// Config is dns's section as the test sets it, decoded into whatever reads it by yaml
// as a node decodes it: dns, and cm's helpers reading the cloud's certificates.
func (n *node) Config(into any) error {
	if err := roundTrip(cloudWide{CM: n.cm}, into); err != nil {
		return err
	}
	var cfg hostConfig
	cfg.Listen = "127.0.0.1:0"
	if n.config != nil {
		if err := n.config(&cfg); err != nil {
			return err
		}
	}
	return roundTrip(cfg, into)
}

// cloudWide is what a node decodes into every section before the section's own.
type cloudWide struct {
	CM cm.Config `yaml:"cm,omitempty"`
}

func roundTrip(from, into any) error {
	raw, err := yaml.Marshal(from)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(raw, into)
}

func newNode(t *testing.T, running ...any) *node {
	t.Helper()
	p := peertest.Mock(t, t.Context())
	t.Cleanup(p.Close)
	n := &node{Node: p, Served: service.NewServed(p.ID(), nil, rpc.Retry{For: 50 * time.Millisecond})}
	for _, r := range running {
		if router, ok := r.(*fakeRouter); ok {
			n.Serve(statsService.Protocol, viewOf{router})
			continue
		}
		n.running = append(n.running, service.Running{Service: r})
	}
	return n
}

// viewOf is a stats view its node serves under stats' protocol.
type viewOf struct{ *fakeRouter }

func (viewOf) Start(context.Context, service.Host) error { return nil }
func (viewOf) Close() error                              { return nil }

// names is a node's catalog.
type names []string

func (n names) ServiceNames() []string { return n }

// databases is the node's shared databases over one store per name, or one that
// cannot open any.
type databases struct {
	f   *kvdbmock.Factory
	err error
}

func (d databases) Database(name string) (kvdb.Instance, error) {
	if d.err != nil {
		return nil, d.err
	}
	return d.f.New(nil, name, 0)
}

// issuing is a cloud that issues certificates.
var issuing = cm.Config{Certificate: cm.CertificateConfig{ACME: certificate.ACME{Enabled: true}}}

func TestDnsStartsFromItsNodeAndAnswersThere(t *testing.T) {
	shared := databases{f: kvdbmock.New()}
	n := newNode(t, builders, names{"build", "auth"}, shared)
	n.cm = issuing
	n.config = func(cfg *hostConfig) error {
		cfg.CloudFqdn = "tau.example.io"
		return nil
	}
	s := New()
	t.Cleanup(func() { s.Close() })
	require.NoError(t, s.Start(t.Context(), n))

	// A DNS-01 VALUE PRESENTED IN THE NODE'S COPY OF THE CHALLENGE STORE, as a cm
	// client on the node presents one, is what dns answers.
	presenting, err := cm.ChallengesOn(t.Context(), n)
	require.NoError(t, err)
	t.Cleanup(func() { presenting.Close() })
	require.NoError(t, presenting.Present(t.Context(), "build.tau.example.io", "proof"))

	addrs := s.Addrs()
	require.Len(t, addrs, 2, "udp and tcp, on the section's address")
	var udp string
	for _, a := range addrs {
		if a.Network() == "udp" {
			udp = a.String()
		}
	}
	ask := func(name string, qtype uint16) *dns.Msg {
		reply, _, err := (&dns.Client{Timeout: 3 * time.Second}).ExchangeContext(t.Context(), query(name, qtype), udp)
		require.NoError(t, err)
		return reply
	}

	reply := ask("build.tau.example.io", dns.TypeA)
	require.Len(t, reply.Answer, 1, "a service the catalog names, routed from the stats view beside it")
	require.Equal(t, "203.0.113.7", reply.Answer[0].(*dns.A).A.String())

	reply = ask("_acme-challenge.build.tau.example.io", dns.TypeTXT)
	require.Len(t, reply.Answer, 1, "DNS-01 from the node's copy of the challenge store")
	require.Equal(t, []string{"proof"}, reply.Answer[0].(*dns.TXT).Txt)

	require.NoError(t, s.Close())
	require.NoError(t, s.Close(), "close is idempotent")
}

func TestACloudIssuingNothingAnswersNoChallenge(t *testing.T) {
	n := newNode(t, builders, names{"build"})
	n.config = func(cfg *hostConfig) error {
		cfg.CloudFqdn = "tau.example.io"
		return nil
	}
	s := New()
	t.Cleanup(func() { s.Close() })
	require.NoError(t, s.Start(t.Context(), n))
	require.Nil(t, s.opts.challenges)
	require.NotNil(t, s.opts.services)
	require.NotNil(t, s.opts.generated)
}

func TestANodeWithNoZoneAnswersNoGeneratedName(t *testing.T) {
	s := New()
	t.Cleanup(func() { s.Close() })
	require.NoError(t, s.Start(t.Context(), newNode(t, builders, names{"build"})))
	require.Nil(t, s.opts.generated)
	require.Nil(t, s.opts.services)
	require.Nil(t, s.opts.zones, "no worker beside it, so no authored records")
}

func TestDnsRefusesWhatItCannotStartWith(t *testing.T) {
	// Each is closed as its instance closes a service whose start failed.
	start := func(n *node) error {
		s := New()
		t.Cleanup(func() { s.Close() })
		return s.Start(t.Context(), n)
	}
	require.ErrorIs(t, start(newNode(t)), ErrNoRouter, "no stats beside it")

	broken := newNode(t, builders)
	broken.config = func(*hostConfig) error { return errors.New("no such key") }
	require.ErrorContains(t, start(broken), "no such key")

	gone := newNode(t, builders, databases{err: errors.New("challenge store gone")})
	gone.cm = issuing
	require.ErrorContains(t, start(gone), "challenge store gone")

	bad := newNode(t, builders)
	bad.config = func(cfg *hostConfig) error {
		cfg.MaxRecords = -1
		return nil
	}
	require.Error(t, start(bad), "a section that does not validate")
}
