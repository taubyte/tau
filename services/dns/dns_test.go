package dns

import (
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/stats"
)

// fakeRouter is a table of nodes and a record of what was asked.
type fakeRouter struct {
	nodes      map[string][]stats.Node
	lastName   string
	lastMaxAge time.Duration
}

func (r *fakeRouter) Route(name string, maxAge time.Duration) []stats.Node {
	r.lastName, r.lastMaxAge = name, maxAge
	return r.nodes[name]
}

func serving(name string, nodes ...stats.Node) *fakeRouter {
	return &fakeRouter{nodes: map[string][]stats.Node{name: nodes}}
}

func newService(t *testing.T, r Router, adjust func(*Config)) *Service {
	t.Helper()
	cfg := Config{Router: r}
	if adjust != nil {
		adjust(&cfg)
	}
	s, err := New(cfg)
	require.NoError(t, err)
	return s
}

func query(name string, qtype uint16) *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	return m
}

// TestAnAnswerIsAStatsReadTurnedIntoRecords, in the order stats gave it —
// least loaded first, because many clients take the first address.
func TestAnAnswerIsAStatsReadTurnedIntoRecords(t *testing.T) {
	r := serving("shop.example.com",
		stats.Node{Peer: "idle", Load: 0.1, Ready: true, Addresses: []string{"203.0.113.1"}},
		stats.Node{Peer: "busy", Load: 0.9, Ready: true, Addresses: []string{"203.0.113.2"}},
	)
	s := newService(t, r, nil)

	reply := s.Answer(query("shop.example.com", dns.TypeA))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.True(t, reply.Authoritative)
	require.Len(t, reply.Answer, 2)

	require.Equal(t, "203.0.113.1", reply.Answer[0].(*dns.A).A.String(),
		"the least loaded node is first, and first is what most clients use")
	require.Equal(t, "203.0.113.2", reply.Answer[1].(*dns.A).A.String())
	require.Equal(t, uint32(30), reply.Answer[0].Header().Ttl)

	require.Equal(t, "shop.example.com", r.lastName,
		"the trailing dot is not part of the name stats knows")
	require.Equal(t, DefaultMaxAge, r.lastMaxAge, "dns states its own staleness tolerance")
}

// TestNothingServingIsServfailAndNeverNxdomain. A name whose nodes are all
// restarting exists and is momentarily unserved; NXDOMAIN is cached negatively
// by every resolver in the path and turns a ten-second gap into minutes.
func TestNothingServingIsServfailAndNeverNxdomain(t *testing.T) {
	s := newService(t, &fakeRouter{nodes: map[string][]stats.Node{}}, nil)

	reply := s.Answer(query("shop.example.com", dns.TypeA))
	require.Equal(t, dns.RcodeServerFailure, reply.Rcode)
	require.NotEqual(t, dns.RcodeNameError, reply.Rcode)
	require.Empty(t, reply.Answer)
}

// TestATypeWeDoNotServeIsNoerrorAndNeverNxdomain. The name exists; this server
// has no MX for it. NXDOMAIN would tell the resolver the NAME is absent, which
// it may then apply to every other type.
func TestATypeWeDoNotServeIsNoerrorAndNeverNxdomain(t *testing.T) {
	s := newService(t, serving("shop.example.com",
		stats.Node{Ready: true, Addresses: []string{"203.0.113.1"}}), nil)

	for _, qtype := range []uint16{dns.TypeMX, dns.TypeTXT, dns.TypeSRV, dns.TypeCNAME} {
		reply := s.Answer(query("shop.example.com", qtype))
		require.Equal(t, dns.RcodeSuccess, reply.Rcode, dns.TypeToString[qtype])
		require.Empty(t, reply.Answer, dns.TypeToString[qtype])
	}
}

// TestAAndAaaaAreAnsweredFromTheSameReport, each taking only the addresses it
// can actually carry.
func TestAAndAaaaAreAnsweredFromTheSameReport(t *testing.T) {
	s := newService(t, serving("shop.example.com", stats.Node{
		Ready: true, Addresses: []string{"203.0.113.1", "2001:db8::1"},
	}), nil)

	v4 := s.Answer(query("shop.example.com", dns.TypeA))
	require.Len(t, v4.Answer, 1)
	require.Equal(t, "203.0.113.1", v4.Answer[0].(*dns.A).A.String())

	v6 := s.Answer(query("shop.example.com", dns.TypeAAAA))
	require.Len(t, v6.Answer, 1)
	require.Equal(t, "2001:db8::1", v6.Answer[0].(*dns.AAAA).AAAA.String())
}

// TestAnAddressThatIsNotAnIpIsSkipped. stats carries what nodes report and
// parses nothing, so a hostname is a legitimate entry and simply cannot be an
// A record.
func TestAnAddressThatIsNotAnIpIsSkipped(t *testing.T) {
	s := newService(t, serving("shop.example.com",
		stats.Node{Peer: "a", Ready: true, Addresses: []string{"edge.example.com", "not an ip at all"}},
		stats.Node{Peer: "b", Ready: true, Addresses: []string{"203.0.113.9"}},
	), nil)

	reply := s.Answer(query("shop.example.com", dns.TypeA))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Len(t, reply.Answer, 1, "the node with no usable address is simply not in the answer")
	require.Equal(t, "203.0.113.9", reply.Answer[0].(*dns.A).A.String())
}

// TestAnAnswerIsCappedSoItFitsADatagram. Truncation sends every resolver back
// over TCP, which is a cost paid on every query to punish a name for being
// popular.
func TestAnAnswerIsCappedSoItFitsADatagram(t *testing.T) {
	var nodes []stats.Node
	for i := range 40 {
		nodes = append(nodes, stats.Node{
			Peer:      string(rune('a' + i%26)),
			Ready:     true,
			Addresses: []string{net.IPv4(203, 0, 113, byte(i+1)).String()},
		})
	}
	s := newService(t, serving("shop.example.com", nodes...), func(c *Config) { c.MaxRecords = 4 })

	reply := s.Answer(query("shop.example.com", dns.TypeA))
	require.Len(t, reply.Answer, 4)
	require.Equal(t, "203.0.113.1", reply.Answer[0].(*dns.A).A.String(),
		"and the cap keeps the FRONT of the list, which is the least loaded end")
}

// TestOneNodeWithManyAddressesStillRespectsTheCap — the inner loop has to break
// out too, not just the outer one.
func TestOneNodeWithManyAddressesStillRespectsTheCap(t *testing.T) {
	var addrs []string
	for i := range 20 {
		addrs = append(addrs, net.IPv4(203, 0, 113, byte(i+1)).String())
	}
	s := newService(t, serving("shop.example.com", stats.Node{Ready: true, Addresses: addrs}),
		func(c *Config) { c.MaxRecords = 3 })

	require.Len(t, s.Answer(query("shop.example.com", dns.TypeA)).Answer, 3)
}

// TestTheNameIsNormalisedBeforeItIsLookedUp. DNS is case-insensitive on the
// wire and stats keys are exact, so a query in mixed case must not miss.
func TestTheNameIsNormalisedBeforeItIsLookedUp(t *testing.T) {
	r := serving("shop.example.com", stats.Node{Ready: true, Addresses: []string{"203.0.113.1"}})
	s := newService(t, r, nil)

	reply := s.Answer(query("SHOP.Example.COM", dns.TypeA))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Len(t, reply.Answer, 1)
	require.Equal(t, "shop.example.com", r.lastName)
	require.Equal(t, "SHOP.Example.COM.", reply.Answer[0].Header().Name,
		"but the record echoes the name as asked, which is what a resolver matches on")
}

func TestAMalformedQuestionIsAFormatError(t *testing.T) {
	s := newService(t, serving("shop.example.com"), nil)

	none := new(dns.Msg)
	require.Equal(t, dns.RcodeFormatError, s.Answer(none).Rcode)

	two := new(dns.Msg)
	two.SetQuestion(dns.Fqdn("a.example.com"), dns.TypeA)
	two.Question = append(two.Question, dns.Question{
		Name: dns.Fqdn("b.example.com"), Qtype: dns.TypeA, Qclass: dns.ClassINET,
	})
	require.Equal(t, dns.RcodeFormatError, s.Answer(two).Rcode,
		"answering the first and ignoring the second is a reply that does not match the request")

	root := new(dns.Msg)
	root.SetQuestion(".", dns.TypeA)
	require.Equal(t, dns.RcodeFormatError, s.Answer(root).Rcode)
}

func TestANonInternetClassIsNotImplemented(t *testing.T) {
	s := newService(t, serving("shop.example.com"), nil)
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn("shop.example.com"), dns.TypeA)
	m.Question[0].Qclass = dns.ClassCHAOS
	require.Equal(t, dns.RcodeNotImplemented, s.Answer(m).Rcode)
}

func TestConfigRefusesWhatItCannotAnswerFrom(t *testing.T) {
	_, err := New(Config{})
	require.ErrorIs(t, err, ErrNoRouter)

	_, err = New(Config{Router: serving("x"), MaxAge: -time.Second})
	require.Error(t, err)

	_, err = New(Config{Router: serving("x"), MaxRecords: -1})
	require.Error(t, err)

	s, err := New(Config{Router: serving("x")})
	require.NoError(t, err)
	require.Equal(t, DefaultTTL, s.cfg.TTL)
	require.Equal(t, DefaultMaxAge, s.cfg.MaxAge)
	require.Equal(t, DefaultMaxRecords, s.cfg.MaxRecords)
}

func TestTheConfiguredTtlReachesTheRecord(t *testing.T) {
	s := newService(t, serving("shop.example.com",
		stats.Node{Ready: true, Addresses: []string{"203.0.113.1"}}),
		func(c *Config) { c.TTL = 5 * time.Second })

	reply := s.Answer(query("shop.example.com", dns.TypeA))
	require.Equal(t, uint32(5), reply.Answer[0].Header().Ttl)
}

// TestItAnswersOverTheWireOnBothTransports. UDP and TCP both, because a
// truncated UDP answer sends every resolver back over TCP — a server listening
// on one fails exactly the largest answers.
func TestItAnswersOverTheWireOnBothTransports(t *testing.T) {
	s := newService(t, serving("shop.example.com",
		stats.Node{Ready: true, Addresses: []string{"203.0.113.1"}}), nil)

	require.NoError(t, s.Listen("127.0.0.1:0"))
	t.Cleanup(func() { s.Close() })

	addrs := s.Addrs()
	require.Len(t, addrs, 2, "one packet conn and one listener")

	for _, addr := range addrs {
		proto := addr.Network()
		c := &dns.Client{Net: proto, Timeout: 5 * time.Second}
		reply, _, err := c.Exchange(query("shop.example.com", dns.TypeA), addr.String())
		require.NoError(t, err, proto)
		require.Equal(t, dns.RcodeSuccess, reply.Rcode, proto)
		require.Len(t, reply.Answer, 1, proto)
		require.Equal(t, "203.0.113.1", reply.Answer[0].(*dns.A).A.String(), proto)
	}
}

func TestListenTwiceIsRefusedAndCloseIsIdempotent(t *testing.T) {
	s := newService(t, serving("shop.example.com"), nil)

	require.NoError(t, s.Listen("127.0.0.1:0"))
	require.Error(t, s.Listen("127.0.0.1:0"))

	require.NoError(t, s.Close())
	require.NoError(t, s.Close())
	require.Error(t, s.Listen("127.0.0.1:0"), "a closed server does not come back")
	require.Empty(t, s.Addrs())
}

func TestListenReportsAFailureToBind(t *testing.T) {
	s := newService(t, serving("shop.example.com"), nil)
	require.Error(t, s.Listen("256.256.256.256:53"))
	s.Close()
}
