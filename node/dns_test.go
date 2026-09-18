package node

import (
	"net"
	"strconv"
	"testing"
	"time"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/project"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

// dns_test closes the loop the proxy opened: a proxy binds a port the kernel
// chose, stats carries it, and a REAL resolver query gets it back. Every link
// asserted on its own would leave the one that matters — that the three agree
// about a name — untested.

// serving is a node that runs the whole chain and answers on an ephemeral port,
// because :53 needs a privilege a test does not have.
func serving(t *testing.T, compiled []byte) *Node {
	t.Helper()

	cfg := Config{Services: []string{Register, Worker, Stats, Proxy, DNS}}
	cfg.Proxy.CloudFqdn = "tau.example.io"
	cfg.DNS.NodeZone = "nodes.tau.example.io"

	// A DEPLOY REACHES A RESOLVER IN TWO HOPS, and both are timers: the proxy
	// notices what this node holds on its sweep, and the report carrying that
	// goes out on the stats tick. Left at their defaults — ten seconds and five
	// — this test would spend fifteen waiting for two clocks, so both are wound
	// down rather than waited out.
	cfg.Proxy.Sweep = 100 * time.Millisecond
	cfg.Stats.Interval = 200 * time.Millisecond

	n, _, _ := makeNode(t, cfg)
	require.NoError(t, n.Start(t.Context()))

	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled,
	})
	require.NoError(t, err)

	_, err = n.Worker().Resolve(t.Context(),
		project.Address{Project: "shop", Environment: "prod", Kind: "application", Name: "db"})
	require.NoError(t, err)

	return n
}

// resolver asks this node's own server, over UDP.
func resolver(t *testing.T, n *Node) func(string, uint16) *dns.Msg {
	t.Helper()

	addrs := n.DNSAddrs()
	require.NotEmpty(t, addrs, "the server bound nothing")

	var at string
	for _, a := range addrs {
		if _, ok := a.(*net.UDPAddr); ok {
			at = a.String()
		}
	}
	require.NotEmpty(t, at, "no udp socket")

	return func(name string, qtype uint16) *dns.Msg {
		t.Helper()

		m := new(dns.Msg)
		m.SetQuestion(dns.Fqdn(name), qtype)

		reply, _, err := (&dns.Client{Timeout: 3 * time.Second}).ExchangeContext(t.Context(), m, at)
		require.NoError(t, err)
		return reply
	}
}

// TestASRVQUERYANSWERSWITHTHEPORTTHEKERNELGAVETHEPROXY. Nothing else can carry
// it: it is not authored, not derivable, and gone when the node restarts.
func TestAResolverLearnsTheProxysPortOverDns(t *testing.T) {
	n := serving(t, withProxy(nil))

	require.Eventually(t, func() bool { return len(n.proxy.Serving()) == 1 }, 5*time.Second, 20*time.Millisecond)
	sock := n.proxy.Serving()[0]

	ask := resolver(t, n)

	// The report has to reach the view first — it is broadcast on a tick.
	var answer *dns.Msg
	require.Eventually(t, func() bool {
		answer = ask("_postgres._tcp."+sock.Host, dns.TypeSRV)
		return answer.Rcode == dns.RcodeSuccess && len(answer.Answer) == 1
	}, 15*time.Second, 100*time.Millisecond, "no SRV answer for the proxy's name")

	srv := answer.Answer[0].(*dns.SRV)
	require.Equal(t, sock.Port, srv.Port, "the port this node's kernel handed the listener")
	require.NotEmpty(t, srv.Target, "an SRV target is a name, and something has to resolve it")

	// AND THAT NAME RESOLVES, which is the half an SRV record cannot do itself.
	require.Contains(t, srv.Target, ".nodes.tau.example.io.")
	require.NotEmpty(t, answer.Extra, "the target's addresses ride along")
}

// TestTHENAMEALSORESOLVESTOTHISNODE. A client resolves the host to an address
// before it dials the port, so a socket published without its name in Serving
// is a port nobody can find the machine for.
func TestTheProxysNameResolvesToTheNodeServingIt(t *testing.T) {
	n := serving(t, withProxy(nil))

	require.Eventually(t, func() bool { return len(n.proxy.Serving()) == 1 }, 5*time.Second, 20*time.Millisecond)
	host := n.proxy.Serving()[0].Host

	ask := resolver(t, n)
	require.Eventually(t, func() bool {
		reply := ask(host, dns.TypeA)
		return reply.Rcode == dns.RcodeSuccess
	}, 15*time.Second, 100*time.Millisecond, "the proxy's own name does not resolve")
}

// TestANAMENOBODYSERVES is SERVFAIL and not NXDOMAIN, because "nothing is
// serving it right now" is not "it does not exist" — and the difference is how
// long a resolver remembers.
func TestANameNothingServesIsServfail(t *testing.T) {
	n := serving(t, withProxy(nil))
	ask := resolver(t, n)

	require.Equal(t, dns.RcodeServerFailure, ask("nothing.example.com", dns.TypeA).Rcode)
}

// TestADNSNODENEEDSTHEVIEWITANSWERSFROM, and says so when it is built rather
// than binding a port and answering with nothing.
func TestDnsWithoutStatsIsRefusedAtConstruction(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	_, err := New(Config{Services: []string{Register, DNS}},
		WithHost(h), WithPubSub(newPubSub(t, h)), WithDatabases(&memFactory{}))
	require.ErrorIs(t, err, ErrNoStatsForDNS)
}

// TestTHEPORTISREPORTEDBACK, which is how anything asking for :0 finds out —
// and what makes a node able to say where it is answering.
func TestADnsNodeSaysWhereItBound(t *testing.T) {
	n := serving(t, withProxy(nil))

	addrs := n.DNSAddrs()
	require.Len(t, addrs, 2, "udp and tcp, always: a truncated answer is retried over tcp")

	for _, a := range addrs {
		_, port, err := net.SplitHostPort(a.String())
		require.NoError(t, err)
		n, err := strconv.Atoi(port)
		require.NoError(t, err)
		require.NotZero(t, n)
	}
}

// TestWHATTHISNODESAYSACLIENTCANREACHITAT. The proto field's own documentation
// says these are the IPs a DNS answer is made of and NOT the multiaddrs the
// fleet gossips over — and the code reported `/ip6/…/tcp/4242` verbatim, so
// every A query answered with no records at all.
func TestTheReportCarriesAddressesAndNotMultiaddrs(t *testing.T) {
	n, h, _ := makeNode(t, Config{Services: []string{Register, Stats}})
	require.NoError(t, n.Start(t.Context()))

	snap, err := n.Snapshot(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, snap.Addresses, "a node that reports nowhere is routed to nowhere")

	for _, a := range snap.Addresses {
		require.NotContains(t, a, "/", "a multiaddr is not an address: %q", a)
		require.NotNil(t, net.ParseIP(a), "%q does not parse as an ip", a)
	}

	// And it is the host's own, not an invention.
	require.LessOrEqual(t, len(snap.Addresses), len(h.Addrs()))
}
