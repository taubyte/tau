//go:build dreaming

package dream_test

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	storageService "github.com/taubyte/tau/services/storage"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
)

// The function arm of a zone end to end over UDP: a resolver named in a zone, found
// through the worker, instantiated in the dns-resolver world, its lines put on the
// wire. The node runs no gateway, as a zone-only project's ingress is a resolver.

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
func asking(t *testing.T, n *instance.Instance) func(string, uint16) *dns.Msg {
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
		instance.Register, instance.Worker, instance.Stats, instance.DNS, storageService.Descriptor.Name)
	return asking(t, r.node)
}

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
