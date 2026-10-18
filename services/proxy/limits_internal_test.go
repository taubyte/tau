package proxy

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// limits_internal_test is about the KEY a limit is counted under, which is not
// reachable from outside and should not be: what it produces is an
// implementation detail, and what matters is which addresses land on the same
// one.

func addr(t *testing.T, s string) net.Addr {
	t.Helper()
	a, err := net.ResolveTCPAddr("tcp", s)
	require.NoError(t, err)
	return a
}

// TestSUBNETCOUNTSONEALLOCATIONONCE. A /64 is the smallest block anyone is
// routinely handed on v6 — a single machine's — so counting per address there
// is counting per connection, and the limit is gone for exactly the caller it
// exists to stop. /24 is the v4 equivalent that is small enough not to sweep up
// an unrelated network.
func TestCountingPerSubnetTreatsOneAllocationAsOneSource(t *testing.T) {
	require.Equal(t, sourceKey(addr(t, "203.0.113.7:51000"), "subnet"), sourceKey(addr(t, "203.0.113.9:51001"), "subnet"),
		"two addresses in one /24 are one source")
	require.NotEqual(t, sourceKey(addr(t, "203.0.113.7:51000"), "subnet"), sourceKey(addr(t, "203.0.114.7:51000"), "subnet"),
		"and a different /24 is a different one")

	require.Equal(t, sourceKey(addr(t, "[2001:db8:1:2::7]:51000"), "subnet"), sourceKey(addr(t, "[2001:db8:1:2:a::9]:51001"), "subnet"),
		"two addresses in one /64 are one source")
	require.NotEqual(t, sourceKey(addr(t, "[2001:db8:1:2::7]:51000"), "subnet"), sourceKey(addr(t, "[2001:db8:1:3::7]:51000"), "subnet"),
		"and a different /64 is a different one")
}

// TestCOUNTINGPERADDRESSKEEPSTHEMAPART, which is the default and the stricter
// reading of "each address separately".
func TestCountingPerIpKeepsTwoAddressesApart(t *testing.T) {
	require.NotEqual(t, sourceKey(addr(t, "203.0.113.7:51000"), "ip"), sourceKey(addr(t, "203.0.113.9:51001"), "ip"))
	require.Equal(t, sourceKey(addr(t, "203.0.113.7:51000"), "ip"), sourceKey(addr(t, "203.0.113.7:51999"), "ip"),
		"a second connection from one address is the same source")
}

// TestAV4INV6ADDRESSISTHEV4ONE. A dual-stack listener reports a v4 client as
// ::ffff:203.0.113.7, and a key that kept that spelling would count one host
// twice and cut its budget in half on a v6 socket.
func TestAV4MappedAddressCountsAsTheV4One(t *testing.T) {
	require.Equal(t, sourceKey(addr(t, "203.0.113.7:51000"), "ip"), sourceKey(addr(t, "[::ffff:203.0.113.7]:51000"), "ip"))
	require.Equal(t, sourceKey(addr(t, "203.0.113.7:51000"), "subnet"), sourceKey(addr(t, "[::ffff:203.0.113.9]:51000"), "subnet"),
		"and its subnet is the /24, not a /64 of mapped addresses")
}

// TestANUNKNOWNKEYCOUNTSEVERYTHINGTOGETHER rather than counting nothing: a
// misspelling that silently disabled the limit would look configured and not be.
func TestAnUnknownCountingKeyFallsBackToTheAddress(t *testing.T) {
	require.Equal(t, sourceKey(addr(t, "203.0.113.7:51000"), ""), sourceKey(addr(t, "203.0.113.7:51000"), "per-tenant"))
	require.NotEmpty(t, sourceKey(addr(t, "203.0.113.7:51000"), "per-tenant"))
}

// TestNOADDRESSISONEKEY rather than a panic: a listener can hand back a
// connection whose peer is already gone.
func TestANilAddressIsCountedUnderOneKey(t *testing.T) {
	require.Empty(t, sourceKey(nil, "ip"))
}

// TestNOBLOCKISNOLIMITATALL, which is what limitsOf reads off a resource that
// wrote none — and what makes the whole limiter skippable on the accept path.
func TestLimitsWithNothingSetAreNoLimits(t *testing.T) {
	require.True(t, limits{}.none())
	require.False(t, limits{rate: 1}.none())
	require.False(t, limits{concurrent: 1}.none())
	require.True(t, limits{burst: 5, per: "subnet"}.none(),
		"a burst and a key with neither ceiling limit nothing")
}
