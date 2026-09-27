//go:build dreaming

package dream_test

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
)

// dns_test drives the arm of a zone that is a FUNCTION, end to end, over UDP.
//
// # Why this is the corpus and not a unit test
//
// Every piece of this has its own test already: the component answers all three
// arms in pkg/invoke/wasm, the server's precedence and rcodes are asserted
// against a fake in services/dns, and the node's walk is exercised in node. What
// none of them can show is that the four of them are wired to each other -- a
// resolver named in a zone, found through the worker like any other function,
// instantiated in the dns-resolver world, its answer parsed as zone-file lines
// and put on the wire as a record.
//
// It is also the first dream test to run node.DNS at all. The node it deploys
// runs NO GATEWAY: this way in is a resolver asking over UDP, so a project whose
// only ingress is a zone is the honest shape to prove it with.

// withDNSApex is the zone the corpus project declares. It must be the published
// host too — the register's index is what maps a name to a deployment, and the
// label walk starts there.
const withDNSApex = "example.com"

// warrenWASM is the dns-resolver fixture: `geo` gets a record, `quiet` exists
// and holds nothing of the type asked, anything else does not exist.
func warrenWASM(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile(filepath.Join("..", "..", "pkg", "invoke", "wasm", "fixtures", "warren.wasm"))
	require.NoError(t, err, "run `cargo component build --release` in pkg/invoke/wasm/fixtures/warren")
	return code
}

// asking asks this node's own server over UDP, which is the only interface a
// real resolver has.
func asking(t *testing.T, n *node.Node) func(string, uint16) *dns.Msg {
	t.Helper()

	var at string
	require.Eventually(t, func() bool {
		for _, a := range n.DNSAddrs() {
			if _, ok := a.(*net.UDPAddr); ok {
				at = a.String()
				return true
			}
		}
		return false
	}, 10*time.Second, 50*time.Millisecond, "the server bound no udp socket")

	return func(name string, qtype uint16) *dns.Msg {
		t.Helper()

		m := new(dns.Msg)
		m.SetQuestion(dns.Fqdn(name), qtype)

		reply, _, err := (&dns.Client{Timeout: 5 * time.Second}).ExchangeContext(t.Context(), m, at)
		require.NoError(t, err)
		return reply
	}
}

func dnsCloud(t *testing.T) func(string, uint16) *dns.Msg {
	t.Helper()
	r := deployServing(t, "with/dns", withDNSApex, warrenWASM(t), nil,
		node.Register, node.Worker, node.Stats, node.DNS)
	return asking(t, r.node)
}

// TestARECORDNOBODYWROTEDOWNISSERVED. The zone declares no records at all, so
// this answer cannot have come from a file — it came from a component that was
// found, started and asked, and whose line was parsed under the zone's $ORIGIN.
func TestAZoneAnsweredByAFunctionServesARealRecord(t *testing.T) {
	ask := dnsCloud(t)

	var reply *dns.Msg
	require.Eventually(t, func() bool {
		reply = ask("geo."+withDNSApex, dns.TypeA)
		return reply.Rcode == dns.RcodeSuccess && len(reply.Answer) == 1
	}, 60*time.Second, 500*time.Millisecond, "the resolver never answered")

	a, ok := reply.Answer[0].(*dns.A)
	require.True(t, ok, "answered a %T", reply.Answer[0])
	require.Equal(t, "203.0.113.9", a.A.String())
	require.Equal(t, "geo."+withDNSApex+".", a.Hdr.Name,
		"the line was relative to the apex and resolved under it")
}

// TestTHETYPEREACHESTHECOMPONENT. warren echoes the type it was asked into the
// record it returns, so an AAAA question coming back as an AAAA record is the
// question having survived the whole chain rather than a fixed reply.
func TestTheQuestionsTypeReachesTheResolver(t *testing.T) {
	ask := dnsCloud(t)

	var reply *dns.Msg
	require.Eventually(t, func() bool {
		reply = ask("geo."+withDNSApex, dns.TypeAAAA)
		return reply.Rcode == dns.RcodeSuccess && len(reply.Answer) == 1
	}, 60*time.Second, 500*time.Millisecond, "the resolver never answered AAAA")

	_, ok := reply.Answer[0].(*dns.AAAA)
	require.True(t, ok, "answered a %T rather than an AAAA", reply.Answer[0])
}

// TestEMPTYANDABSENTARRIVEDIFFERENTLYONTHEWIRE, which is the distinction the
// whole three-armed answer exists for. Both carry no records; only one of them
// tells a resolver the name is gone, and that one is cached as an absence.
func TestEmptyAndAbsentReachTheWireAsDifferentRcodes(t *testing.T) {
	ask := dnsCloud(t)

	// Wait for the deployment to be answering at all before comparing arms.
	require.Eventually(t, func() bool {
		return ask("geo."+withDNSApex, dns.TypeA).Rcode == dns.RcodeSuccess
	}, 60*time.Second, 500*time.Millisecond, "the resolver never came up")

	quiet := ask("quiet."+withDNSApex, dns.TypeA)
	require.Equal(t, dns.RcodeSuccess, quiet.Rcode, "`empty` is NOERROR")
	require.Empty(t, quiet.Answer, "with no records")

	gone := ask("nothing."+withDNSApex, dns.TypeA)
	require.Equal(t, dns.RcodeNameError, gone.Rcode, "`absent` is NXDOMAIN")
	require.Empty(t, gone.Answer)
}
