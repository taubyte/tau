package proxy

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// What matters about the key is which addresses land on the same one.

func addr(t *testing.T, s string) net.Addr {
	t.Helper()
	a, err := net.ResolveTCPAddr("tcp", s)
	require.NoError(t, err)
	return a
}

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

func TestCountingPerIpKeepsTwoAddressesApart(t *testing.T) {
	require.NotEqual(t, sourceKey(addr(t, "203.0.113.7:51000"), "ip"), sourceKey(addr(t, "203.0.113.9:51001"), "ip"))
	require.Equal(t, sourceKey(addr(t, "203.0.113.7:51000"), "ip"), sourceKey(addr(t, "203.0.113.7:51999"), "ip"),
		"a second connection from one address is the same source")
}

func TestAV4MappedAddressCountsAsTheV4One(t *testing.T) {
	require.Equal(t, sourceKey(addr(t, "203.0.113.7:51000"), "ip"), sourceKey(addr(t, "[::ffff:203.0.113.7]:51000"), "ip"))
	require.Equal(t, sourceKey(addr(t, "203.0.113.7:51000"), "subnet"), sourceKey(addr(t, "[::ffff:203.0.113.9]:51000"), "subnet"),
		"and its subnet is the /24, not a /64 of mapped addresses")
}

func TestAnUnknownCountingKeyFallsBackToTheAddress(t *testing.T) {
	require.Equal(t, sourceKey(addr(t, "203.0.113.7:51000"), ""), sourceKey(addr(t, "203.0.113.7:51000"), "per-tenant"))
	require.NotEmpty(t, sourceKey(addr(t, "203.0.113.7:51000"), "per-tenant"))
}

func TestANilAddressIsCountedUnderOneKey(t *testing.T) {
	require.Empty(t, sourceKey(nil, "ip"))
}

func TestLimitsWithNothingSetAreNoLimits(t *testing.T) {
	require.True(t, limits{}.none())
	require.False(t, limits{rate: 1}.none())
	require.False(t, limits{concurrent: 1}.none())
	require.True(t, limits{burst: 5, per: "subnet"}.none(),
		"a burst and a key with neither ceiling limit nothing")
}
