package dns

import (
	"errors"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/rpc"
)

// node is what dns starts on: a peer, what runs beside it, and its section.
type node struct {
	peer.Node
	*service.Served
	running []service.Running
	config  func(*hostConfig) error
}

func (n *node) Peer() peer.Node            { return n.Node }
func (n *node) Running() []service.Running { return n.running }
func (*node) Root() string                 { return "" }
func (*node) Cloud() string                { return "dns-test" }
func (*node) Region() string               { return "" }
func (*node) Now() time.Time               { return time.Now() }
func (n *node) Config(into any) error {
	cfg := into.(*hostConfig)
	cfg.Listen = "127.0.0.1:0"
	if n.config == nil {
		return nil
	}
	return n.config(cfg)
}

func newNode(t *testing.T, running ...any) *node {
	t.Helper()
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	n := &node{Node: p, Served: service.NewServed(p.ID(), nil, rpc.Retry{For: 50 * time.Millisecond})}
	for _, r := range running {
		n.running = append(n.running, service.Running{Service: r})
	}
	return n
}

// names is a node's catalog.
type names []string

func (n names) ServiceNames() []string { return n }

// proving is a node's certificate side, holding challenges.
type proving struct {
	challenges Challenges
	err        error
}

func (p proving) Challenges() (Challenges, error) { return p.challenges, p.err }

func TestDnsStartsFromItsNodeAndAnswersThere(t *testing.T) {
	values := presented{values: map[string][]string{"build.tau.example.io": {"proof"}}}
	n := newNode(t, builders, names{"build", "auth"}, proving{challenges: values})
	n.config = func(cfg *hostConfig) error {
		cfg.CloudFqdn = "tau.example.io"
		return nil
	}
	s := New()
	t.Cleanup(func() { s.Close() })
	require.NoError(t, s.Start(t.Context(), n))

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
	require.Len(t, reply.Answer, 1, "DNS-01 from the node's certificate side")
	require.Equal(t, []string{"proof"}, reply.Answer[0].(*dns.TXT).Txt)

	require.NoError(t, s.Close())
	require.NoError(t, s.Close(), "close is idempotent")
}

func TestACloudIssuingNothingAnswersNoChallenge(t *testing.T) {
	n := newNode(t, builders, names{"build"}, proving{})
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

	require.ErrorContains(t, start(newNode(t, builders, proving{err: errors.New("challenge store gone")})), "challenge store gone")

	bad := newNode(t, builders)
	bad.config = func(cfg *hostConfig) error {
		cfg.MaxRecords = -1
		return nil
	}
	require.Error(t, start(bad), "a section that does not validate")
}
