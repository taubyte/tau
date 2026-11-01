package node

import (
	"encoding/json"
	"net"
	"strconv"
	"testing"
	"time"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"

	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
)

// The proxy's port, carried by discovery, comes back from a real resolver query.

// serving is a node that runs the whole chain and answers on an ephemeral port,
// because :53 needs a privilege a test does not have.
func serving(t *testing.T, compiled []byte, hosts ...string) *Node {
	t.Helper()

	cfg := Config{Services: []string{Register, Worker, Discovery, Proxy, DNS}}
	cfg.Proxy.CloudFqdn = "tau.example.io"
	cfg.DNS.NodeZone = "nodes.tau.example.io"

	// Both timers (proxy sweep and discovery tick) are wound down rather than waited out.
	cfg.Proxy.Sweep = 100 * time.Millisecond
	cfg.Discovery.Interval = 200 * time.Millisecond

	n, _, _ := makeNode(t, cfg)
	require.NoError(t, n.Start(t.Context()))

	// THE HOSTS ARE WHAT TURNS A QUERY INTO A PROJECT. A deploy publishes
	// them beside the object — see services/build/deploy — and a zone's apex
	// is one of them, which is the only reason dns can find the records an
	// author wrote.
	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled, Hosts: hosts,
	})
	require.NoError(t, err)

	// A deployment is only visible to what this node SERVES once the worker
	// holds it, so it is touched here the way a first request would.
	_, err = n.Worker().Resolve(t.Context(),
		specs.Address{Project: "shop", Environment: "prod", Kind: "application", Name: "db"})
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

func TestANameNothingServesIsServfail(t *testing.T) {
	n := serving(t, withProxy(nil))
	ask := resolver(t, n)

	require.Equal(t, dns.RcodeServerFailure, ask("nothing.example.com", dns.TypeA).Rcode)
}

func TestDnsWithoutStatsIsRefusedAtConstruction(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	_, err := New(Config{Services: []string{Register, DNS}},
		WithHost(h), WithPubSub(newPubSub(t, h)), WithDatabases(&memFactory{}))
	require.ErrorIs(t, err, ErrNoDiscoveryForDNS)
}

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

func TestTheReportCarriesAddressesAndNotMultiaddrs(t *testing.T) {
	n, h, _ := makeNode(t, Config{Services: []string{Register, Discovery}})
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

// withZone is a project declaring a zone it owns, and the records in it.
func withZone(apex string, records []any) []byte {
	doc := map[string]any{
		"object": map[string]any{
			"id":   projectID,
			"name": "shop",
			// The application is what the worker is asked to hold — see
			// serving. A zone declares no workload of its own.
			"application": map[string]any{
				proxyAppID: map[string]any{
					"name":   "db",
					"source": map[string]any{"kind": "image", "image": "ghcr.io/acme/postgres:16"},
				},
			},
			"zone": map[string]any{
				zoneResourceID: map[string]any{"name": "own", "fqdn": apex, "records": records},
			},
		},
		"indexes": map[string]any{
			"projects/" + projectID + "/application/db/links": []any{
				"branches/main/projects/" + projectID + "/application/" + proxyAppID},
			"projects/" + projectID + "/zone/own/links": []any{
				"branches/main/projects/" + projectID + "/zone/" + zoneResourceID},
		},
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return raw
}

const zoneResourceID = "QmU1Vy6xqpYjRFFZqucyZULup6svHhsajomz71zWnSz9gk"

func TestAResolverGetsTheRecordsAProjectDeclares(t *testing.T) {
	n := serving(t, withZone("example.com", []any{
		"@ 3600 IN MX 10 mail.example.com.",
		"www 300 IN A 1.2.3.4",
	}), "example.com")

	ask := resolver(t, n)

	mx := ask("example.com", dns.TypeMX)
	require.Equal(t, dns.RcodeSuccess, mx.Rcode)
	require.Len(t, mx.Answer, 1)
	require.Equal(t, "mail.example.com.", mx.Answer[0].(*dns.MX).Mx)

	a := ask("www.example.com", dns.TypeA)
	require.Len(t, a.Answer, 1)
	require.Equal(t, "1.2.3.4", a.Answer[0].(*dns.A).A.String())
}

func TestANameDeepInAZoneFindsIt(t *testing.T) {
	n := serving(t, withZone("example.com", []any{"a.b.c 300 IN TXT \"deep\""}), "example.com")

	reply := resolver(t, n)("a.b.c.example.com", dns.TypeTXT)
	require.Equal(t, dns.RcodeSuccess, reply.Rcode)
	require.Len(t, reply.Answer, 1)
	require.Equal(t, []string{"deep"}, reply.Answer[0].(*dns.TXT).Txt)
}

func TestEveryServiceANodeCanRunIsAName(t *testing.T) {
	n := &Node{opts: options{extra: []Service{{Name: "billing"}}}}
	names := n.knownServices()
	for _, want := range append(append([]string(nil), All...), BuildFrontend, "billing") {
		require.Contains(t, names, want)
	}
}
