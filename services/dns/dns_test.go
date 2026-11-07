package dns

import (
	"bytes"
	"crypto/sha256"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/mr-tron/base58"

	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/stats"
)

// fakeRouter is a table of nodes and a record of what was asked.
//
// IT IS LOCKED because the wire test drives the server over both transports and
// the handlers run on the dns library's own goroutines. The real router is
// services/discovery, which is mutex-guarded for the same reason.
type fakeRouter struct {
	mu         sync.Mutex
	nodes      map[string][]stats.Node
	lastName   string
	lastMaxAge time.Duration
}

func (r *fakeRouter) Route(name string, maxAge time.Duration) []stats.Node {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastName, r.lastMaxAge = name, maxAge
	return r.nodes[name]
}

func (r *fakeRouter) asked() (string, time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastName, r.lastMaxAge
}

func (r *fakeRouter) Get(peer string) (stats.Node, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ns := range r.nodes {
		for _, n := range ns {
			if n.Peer == peer {
				return n, true
			}
		}
	}
	return stats.Node{}, false
}

func serving(name string, nodes ...stats.Node) *fakeRouter {
	return &fakeRouter{nodes: map[string][]stats.Node{name: nodes}}
}

func newService(t *testing.T, r Router, adjust func(*Config)) *Service {
	t.Helper()
	var cfg Config
	if adjust != nil {
		adjust(&cfg)
	}
	s, err := New(cfg, WithRouter(r))
	require.NoError(t, err)
	return s
}

func query(name string, qtype uint16) *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	return m
}

// TestAnAnswerIsAStatsReadTurnedIntoRecords, in the order discovery gave it —
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

	name, maxAge := r.asked()
	require.Equal(t, "shop.example.com", name,
		"the trailing dot is not part of the name stats knows")
	require.Equal(t, DefaultMaxAge, maxAge, "dns states its own staleness tolerance")
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

// TestAnAddressThatIsNotAnIpIsSkipped. discovery carries what nodes report and
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
// wire and discovery keys are exact, so a query in mixed case must not miss.
func TestTheNameIsNormalisedBeforeItIsLookedUp(t *testing.T) {
	r := serving("shop.example.com", stats.Node{Ready: true, Addresses: []string{"203.0.113.1"}})
	s := newService(t, r, nil)

	reply := s.Answer(query("SHOP.Example.COM", dns.TypeA))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Len(t, reply.Answer, 1)
	name, _ := r.asked()
	require.Equal(t, "shop.example.com", name)
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
	require.ErrorIs(t, err, ErrNoRouter, "a server with no router is refused at construction")

	_, err = New(Config{}, WithRouter(nil))
	require.ErrorIs(t, err, ErrNoRouter)

	_, err = New(Config{MaxAge: -time.Second}, WithRouter(serving("x")))
	require.Error(t, err)

	_, err = New(Config{MaxRecords: -1}, WithRouter(serving("x")))
	require.Error(t, err)

	s, err := New(Config{}, WithRouter(serving("x")))
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

// A workload's own port never leaves its node: the container engine allocates a
// host port and a proxy on that node forwards to it over p2p. Nobody authors
// the port a client dials either — the kernel gives it to whichever listener
// asked — so SRV is the only way a client ever learns it, and an A record
// structurally cannot carry it.

func srvService(name string, nodes ...stats.Node) *fakeRouter {
	return &fakeRouter{nodes: map[string][]stats.Node{name: nodes}}
}

func withZone(c *Config) { c.NodeZone = "nodes.example.com" }

// on is one listener this node reports, in the terms an SRV query asks in.
func on(host, protocol string, port uint16) []stats.Socket {
	return []stats.Socket{{Host: host, Protocol: protocol, Transport: "tcp", Port: port}}
}

func TestSrvCarriesTheProxyPortAndPointsAtTheNode(t *testing.T) {
	s := newService(t, srvService("shop.example.com",
		stats.Node{Peer: peerNamed(t, "idle"), Load: 0.1, Ready: true, Sockets: on("shop.example.com", "https", 8443),
			Addresses: []string{"203.0.113.1"}},
		stats.Node{Peer: peerNamed(t, "busy"), Load: 0.9, Ready: true, Sockets: on("shop.example.com", "https", 8443),
			Addresses: []string{"203.0.113.2"}},
	), withZone)

	reply := s.Answer(query("_https._tcp.shop.example.com", dns.TypeSRV))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Len(t, reply.Answer, 2)

	first := reply.Answer[0].(*dns.SRV)
	require.Equal(t, uint16(8443), first.Port, "the port an A record cannot carry")
	require.Equal(t, nodeLabel(peerNamed(t, "idle"))+".nodes.example.com.", first.Target)
	require.Equal(t, uint16(0), first.Priority, "one priority, so weight is what decides")

	second := reply.Answer[1].(*dns.SRV)
	require.Greater(t, first.Weight, second.Weight,
		"the idle node gets the larger share — the load ordering survives into the answer")
}

// TestTheSrvTargetsAddressesRideAlong, so a client does not pay a second round
// trip for something this server already had in hand.
func TestTheSrvTargetsAddressesRideAlong(t *testing.T) {
	s := newService(t, srvService("shop.example.com",
		stats.Node{Peer: peerNamed(t, "one"), Ready: true, Sockets: on("shop.example.com", "https", 9000),
			Addresses: []string{"203.0.113.7", "2001:db8::1"}},
	), withZone)

	reply := s.Answer(query("_https._tcp.shop.example.com", dns.TypeSRV))
	require.Len(t, reply.Answer, 1)
	require.Len(t, reply.Extra, 2, "one A and one AAAA for the target")

	var names []string
	for _, rr := range reply.Extra {
		names = append(names, rr.Header().Name)
	}
	want := nodeLabel(peerNamed(t, "one")) + ".nodes.example.com."
	require.Equal(t, []string{want, want}, names)
}

// TestANodeNameResolvesToThatNodesAddresses. An SRV target is a NAME, so
// something has to resolve it — and it resolves to that node and nothing else.
func TestANodeNameResolvesToThatNodesAddresses(t *testing.T) {
	s := newService(t, srvService("shop.example.com",
		stats.Node{Peer: peerNamed(t, "one"), Ready: true, Sockets: on("shop.example.com", "https", 9000), Addresses: []string{"203.0.113.7"}},
		stats.Node{Peer: peerNamed(t, "two"), Ready: true, Sockets: on("shop.example.com", "https", 9000), Addresses: []string{"203.0.113.8"}},
	), withZone)

	reply := s.Answer(query(nodeLabel(peerNamed(t, "two"))+".nodes.example.com", dns.TypeA))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Len(t, reply.Answer, 1)
	require.Equal(t, "203.0.113.8", reply.Answer[0].(*dns.A).A.String(),
		"one node's name is one node's addresses, not the service's")
}

func TestAnUnknownNodeNameIsServfail(t *testing.T) {
	s := newService(t, srvService("shop.example.com",
		stats.Node{Peer: peerNamed(t, "one"), Ready: true, Sockets: on("shop.example.com", "https", 9000), Addresses: []string{"203.0.113.7"}},
	), withZone)

	reply := s.Answer(query(nodeLabel(peerNamed(t, "ghost"))+".nodes.example.com", dns.TypeA))
	require.Equal(t, dns.RcodeServerFailure, reply.Rcode)
}

// TestANodeWithNoListenerForThisServiceIsNotAnSrvAnswer. A node still warming
// up reports no socket for the name; routing to it would be routing to a
// refusal.
func TestANodeWithNoProxyPortIsNotAnSrvAnswer(t *testing.T) {
	s := newService(t, srvService("shop.example.com",
		stats.Node{Peer: peerNamed(t, "up"), Ready: true, Sockets: on("shop.example.com", "https", 8443), Addresses: []string{"203.0.113.1"}},
		stats.Node{Peer: peerNamed(t, "warming"), Ready: true, Sockets: nil, Addresses: []string{"203.0.113.2"}},
	), withZone)

	reply := s.Answer(query("_https._tcp.shop.example.com", dns.TypeSRV))
	require.Len(t, reply.Answer, 1)
	require.Equal(t, nodeLabel(peerNamed(t, "up"))+".nodes.example.com.", reply.Answer[0].(*dns.SRV).Target)
}

func TestSrvWithNothingServingIsServfail(t *testing.T) {
	s := newService(t, &fakeRouter{nodes: map[string][]stats.Node{}}, withZone)
	reply := s.Answer(query("_https._tcp.shop.example.com", dns.TypeSRV))
	require.Equal(t, dns.RcodeServerFailure, reply.Rcode)

	// And when every serving node lacks a port, which is the same outcome for a
	// different reason.
	s2 := newService(t, srvService("shop.example.com",
		stats.Node{Peer: peerNamed(t, "p"), Ready: true, Sockets: nil, Addresses: []string{"203.0.113.1"}}), withZone)
	require.Equal(t, dns.RcodeServerFailure,
		s2.Answer(query("_https._tcp.shop.example.com", dns.TypeSRV)).Rcode)
}

// TestSrvIsOffWithoutANodeZone. A target in a zone nobody delegated resolves to
// nothing, so the honest answer is no records rather than an invented name.
func TestSrvIsOffWithoutANodeZone(t *testing.T) {
	s := newService(t, srvService("shop.example.com",
		stats.Node{Peer: peerNamed(t, "p"), Ready: true, Sockets: on("shop.example.com", "https", 8443), Addresses: []string{"203.0.113.1"}}), nil)

	reply := s.Answer(query("_https._tcp.shop.example.com", dns.TypeSRV))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Empty(t, reply.Answer)
}

// TestTheServiceNameIsWhatStatsKnows — the two underscore labels are the service
// and protocol, and the rest is the name a workload is deployed under.
func TestTheServiceNameIsWhatStatsKnows(t *testing.T) {
	r := srvService("shop.example.com",
		stats.Node{Peer: peerNamed(t, "p"), Ready: true, Sockets: on("shop.example.com", "https", 8443), Addresses: []string{"203.0.113.1"}})
	s := newService(t, r, withZone)

	s.Answer(query("_https._tcp.shop.example.com", dns.TypeSRV))
	name, _ := r.asked()
	require.Equal(t, "shop.example.com", name,
		"stats is asked about the workload, not about the underscore labels")
}

func TestAnSrvAnswerIsCapped(t *testing.T) {
	var nodes []stats.Node
	for i := range 20 {
		nodes = append(nodes, stats.Node{
			Peer: peerNamed(t, fmt.Sprintf("peer%02d", i)), Ready: true, Sockets: on("shop.example.com", "https", 8443),
			Addresses: []string{net.IPv4(203, 0, 113, byte(i+1)).String()},
		})
	}
	s := newService(t, srvService("shop.example.com", nodes...), func(c *Config) {
		withZone(c)
		c.MaxRecords = 3
	})

	require.Len(t, s.Answer(query("_https._tcp.shop.example.com", dns.TypeSRV)).Answer, 3)
}

func TestWeightNeverReachesZero(t *testing.T) {
	// RFC 2782 treats weight 0 as "use only if nothing else is available", and
	// this server already filters out nodes it does not want traffic on.
	require.Equal(t, uint16(1), weightOf(1.0))
	require.Equal(t, uint16(1), weightOf(0.999))
	require.Equal(t, uint16(100), weightOf(0))
	require.Greater(t, weightOf(0.1), weightOf(0.9))
}

// TestANodeLabelSurvivesCaseFolding. A peer id is case-sensitive base58 and a
// DNS label is not — a resolver may change the case of any name it forwards, so
// a raw peer id in a label comes back as a different string and matches nothing.
// This is what the node-name test caught before the encoding existed.
func TestANodeLabelSurvivesCaseFolding(t *testing.T) {
	peer := peerNamed(t, "MixedCase")
	label := nodeLabel(peer)

	require.Equal(t, strings.ToLower(label), label, "the label is already lowercase")

	s := newService(t, srvService("shop.example.com",
		stats.Node{Peer: peer, Ready: true, Sockets: on("shop.example.com", "https", 8443), Addresses: []string{"203.0.113.5"}}), withZone)

	// A resolver that upper-cases the name on the way through still resolves it.
	reply := s.Answer(query(strings.ToUpper(label)+".NODES.EXAMPLE.COM", dns.TypeA))
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Len(t, reply.Answer, 1)
	require.Equal(t, "203.0.113.5", reply.Answer[0].(*dns.A).A.String())
}

func TestAnUndecodableNodeLabelIsNotANodeName(t *testing.T) {
	s := newService(t, srvService("shop.example.com",
		stats.Node{Peer: peerNamed(t, "p"), Ready: true, Sockets: on("shop.example.com", "https", 8443), Addresses: []string{"203.0.113.1"}}), withZone)

	// Not base32 at all — it must fall through to the ordinary service lookup
	// rather than being read as a mangled peer id.
	reply := s.Answer(query("not!valid!base32.nodes.example.com", dns.TypeA))
	require.Equal(t, dns.RcodeServerFailure, reply.Rcode)
}

// TestAPeerIdTooLongForALabelGetsNoSrvTarget rather than a truncated one that
// would resolve to somebody else.
func TestAPeerIdTooLongForALabelGetsNoSrvTarget(t *testing.T) {
	// NOT AN ID LIBP2P WOULD MINT — every one of those fits now, which is the
	// point of the fix. This is 50 bytes of valid base58, which is what the
	// guard is there for: an identity nobody has thought of yet.
	long := base58.Encode(bytes.Repeat([]byte{0xAB}, 50))
	require.Greater(t, len(nodeLabel(long)), maxLabel, "the fixture has to actually be too long")

	ok := peerNamed(t, "ok")
	s := newService(t, srvService("shop.example.com",
		stats.Node{Peer: long, Ready: true, Sockets: on("shop.example.com", "https", 8443), Addresses: []string{"203.0.113.1"}},
		stats.Node{Peer: ok, Ready: true, Sockets: on("shop.example.com", "https", 8443), Addresses: []string{"203.0.113.2"}},
	), withZone)

	reply := s.Answer(query("_https._tcp.shop.example.com", dns.TypeSRV))
	require.Len(t, reply.Answer, 1)
	require.Equal(t, nodeLabel(ok)+".nodes.example.com.", reply.Answer[0].(*dns.SRV).Target)
}

// TestONENAMETWOLISTENERS is what a single port per node could not express: a
// postgres proxy and an https gateway on `db.example.com` are two listeners on
// two ports, and the underscore labels are the only thing that tells them
// apart — a raw TCP stream carries no name to demultiplex on.
func TestTwoServicesOnOneNameAnswerWithTheirOwnPorts(t *testing.T) {
	s := newService(t, srvService("db.example.com",
		stats.Node{Peer: peerNamed(t, "both"), Ready: true, Addresses: []string{"203.0.113.9"},
			Sockets: []stats.Socket{
				{Host: "db.example.com", Protocol: "https", Transport: "tcp", Port: 8443},
				{Host: "db.example.com", Protocol: "postgres", Transport: "tcp", Port: 41337},
			}},
	), withZone)

	pg := s.Answer(query("_postgres._tcp.db.example.com", dns.TypeSRV))
	require.Len(t, pg.Answer, 1)
	require.Equal(t, uint16(41337), pg.Answer[0].(*dns.SRV).Port)

	web := s.Answer(query("_https._tcp.db.example.com", dns.TypeSRV))
	require.Len(t, web.Answer, 1)
	require.Equal(t, uint16(8443), web.Answer[0].(*dns.SRV).Port)

	none := s.Answer(query("_redis._tcp.db.example.com", dns.TypeSRV))
	require.Equal(t, dns.RcodeServerFailure, none.Rcode, "nothing here speaks redis")
}

// TestAPROXYONANOTHERPROJECTSNAME: a node runs proxies for every project it
// holds, so the host is matched too — service alone would hand out one
// project's port under another project's name.
func TestASocketForAnotherNameIsNotAnAnswer(t *testing.T) {
	s := newService(t, srvService("db.example.com",
		stats.Node{Peer: peerNamed(t, "mixed"), Ready: true, Addresses: []string{"203.0.113.9"},
			Sockets: on("other.example.com", "postgres", 41337)},
	), withZone)

	reply := s.Answer(query("_postgres._tcp.db.example.com", dns.TypeSRV))
	require.Equal(t, dns.RcodeServerFailure, reply.Rcode)
}

// TestANAMEWITHONEUNDERSCORELABEL is not an SRV question, and answering it as
// one would route `_tcp.example.com` to whatever `_tcp` happened to match.
func TestAMalformedSrvNameIsAFormatError(t *testing.T) {
	s := newService(t, srvService("example.com",
		stats.Node{Peer: peerNamed(t, "p"), Ready: true, Addresses: []string{"203.0.113.1"},
			Sockets: on("example.com", "tcp", 8443)}), withZone)

	for _, name := range []string{"_tcp.example.com", "_postgres.tcp.example.com", "_postgres._tcp."} {
		reply := s.Answer(query(name, dns.TypeSRV))
		require.Equal(t, dns.RcodeFormatError, reply.Rcode, name)
	}
}

// peerNamed mints a REAL peer id, deterministically, from a readable name.
//
// EVERY PEER IN THIS FILE USED TO BE A STRING LIKE "p" OR "peer00", and that is
// exactly how a 74-character label — past DNS's limit of 63, on every id libp2p
// mints — survived here: the encoding was only ever exercised on inputs no
// fleet produces, so the guard that drops an over-long label dropped nothing in
// a test and everything in a cloud.
//
// The seed is the name, so a case reads as `peerNamed(t, "idle")` and still
// carries an id with the shape and the length of a real one.
func peerNamed(t *testing.T, name string) string {
	t.Helper()

	seed := sha256.Sum256([]byte(name))
	priv, _, err := crypto.GenerateEd25519Key(bytes.NewReader(seed[:]))
	require.NoError(t, err)

	id, err := peer.IDFromPrivateKey(priv)
	require.NoError(t, err)
	return id.String()
}

// TestAREALPEERIDFITSINALABEL, with the margin stated rather than assumed.
func TestARealPeerIdBecomesALabelDnsCanCarry(t *testing.T) {
	for _, name := range []string{"idle", "busy", "one", "two"} {
		label := nodeLabel(peerNamed(t, name))
		require.LessOrEqual(t, len(label), maxLabel, "label %q is %d characters", label, len(label))
		require.Equal(t, 61, len(label), "an ed25519 id, which is what libp2p mints by default")
	}
}

// TestTHELABELROUNDTRIPS, which is the whole contract: an SRV target is a name,
// and this server has to resolve that name back to the node that minted it.
func TestALabelDecodesBackToTheSamePeer(t *testing.T) {
	s := newService(t, srvService("shop.example.com"), withZone)

	id := peerNamed(t, "roundtrip")
	back, ok := s.peerOf(nodeLabel(id) + ".nodes.example.com")
	require.True(t, ok)
	require.Equal(t, id, back)
}

// TestANIDTHISSERVERCANNOTSPELL produces no target rather than one derived from
// a spelling it did not understand — which would resolve to a different peer.
func TestAPeerThatIsNotBase58GetsNoLabel(t *testing.T) {
	require.Empty(t, nodeLabel("peer00"), "0 is not in the base58 alphabet")
	require.Empty(t, nodeLabel("not a peer id"))
}

// TestAMappedAddressIsAnARecordAndAZonedOneIsNone: an IPv4 address written in its
// IPv6 form answers A, as net.ParseIP's To4 did, and a link-local address with a
// zone answers nothing anywhere else.
func TestAMappedAddressIsAnARecordAndAZonedOneIsNone(t *testing.T) {
	s := newService(t, serving("shop.example.com",
		stats.Node{Peer: "a", Ready: true, Addresses: []string{"::ffff:203.0.113.7", "fe80::1%eth0", "2001:db8::5"}},
	), nil)

	a := s.Answer(query("shop.example.com", dns.TypeA)).Answer
	require.Len(t, a, 1)
	require.Equal(t, "203.0.113.7", a[0].(*dns.A).A.String())

	aaaa := s.Answer(query("shop.example.com", dns.TypeAAAA)).Answer
	require.Len(t, aaaa, 1, "the zoned address is not an answer")
	require.Equal(t, "2001:db8::5", aaaa[0].(*dns.AAAA).AAAA.String())
}

// TestManyAddressesUnderALargeCapAreEachTheirOwn: past the addresses held on the
// stack, and every record keeps its own address.
func TestManyAddressesUnderALargeCapAreEachTheirOwn(t *testing.T) {
	var addrs []string
	for i := range 50 {
		addrs = append(addrs, net.IPv4(203, 0, 113, byte(i+1)).String(), fmt.Sprintf("2001:db8::%x", i+1))
	}
	s := newService(t, serving("shop.example.com", stats.Node{Ready: true, Addresses: addrs}),
		func(c *Config) { c.MaxRecords = 64 })

	a := s.Answer(query("shop.example.com", dns.TypeA)).Answer
	require.Len(t, a, 50)
	aaaa := s.Answer(query("shop.example.com", dns.TypeAAAA)).Answer
	require.Len(t, aaaa, 50)
	for i := range 50 {
		require.Equal(t, net.IPv4(203, 0, 113, byte(i+1)).String(), a[i].(*dns.A).A.String())
		require.Equal(t, fmt.Sprintf("2001:db8::%x", i+1), aaaa[i].(*dns.AAAA).AAAA.String())
	}
}

func TestAnAnswersRecordsAreThreeAllocations(t *testing.T) {
	var nodes []stats.Node
	for i := range 8 {
		nodes = append(nodes, stats.Node{Ready: true, Addresses: []string{net.IPv4(203, 0, 113, byte(i+1)).String(), fmt.Sprintf("2001:db8::%x", i+1)}})
	}
	s := newService(t, serving("shop.example.com"), nil)
	require.LessOrEqual(t, testing.AllocsPerRun(100, func() { s.records(nodes, "shop.example.com.", false) }), 3.0,
		"the record list, the records and their addresses")
	require.LessOrEqual(t, testing.AllocsPerRun(100, func() { s.records(nodes, "shop.example.com.", true) }), 3.0)
}
