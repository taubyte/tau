package proxy_test

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/services/v2/proxy"
)

func p64(t *testing.T) netip.Prefix {
	t.Helper()
	p, err := netip.ParsePrefix("2001:db8:1:2::/64")
	require.NoError(t, err)
	return p
}

// TestTheSameResourceAlwaysLandsOnTheSameAddress — the property that makes this
// a derivation rather than an allocation. Every node computes it without asking
// anyone, and a redeploy of the same resource keeps its address.
func TestAnAddressIsStable(t *testing.T) {
	first, err := proxy.AddressIn(p64(t), "QmProxy")
	require.NoError(t, err)
	again, err := proxy.AddressIn(p64(t), "QmProxy")
	require.NoError(t, err)
	require.Equal(t, first, again)
}

// TestTwoResourcesDoNotShareAnAddress.
func TestDifferentResourcesDiffer(t *testing.T) {
	a, err := proxy.AddressIn(p64(t), "QmOne")
	require.NoError(t, err)
	b, err := proxy.AddressIn(p64(t), "QmTwo")
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}

// TestTheADDRESSIsInsideTheOperatorsPrefix. A derivation that wrote outside it
// would hand out addresses nobody routes.
func TestAnAddressStaysInsideThePrefix(t *testing.T) {
	prefix := p64(t)
	for i := range 500 {
		got, err := proxy.AddressIn(prefix, fmt.Sprintf("Qm%d", i))
		require.NoError(t, err)
		require.True(t, prefix.Contains(got), "%s escaped %s", got, prefix)
	}
}

// TestNOTWOOfFiveHundredCollide, which is the claim the whole design rests on:
// 64 bits is enough room that an allocator would be bookkeeping for nothing.
func TestFiveHundredResourcesDoNotCollide(t *testing.T) {
	prefix := p64(t)
	seen := map[netip.Addr]string{}

	for i := range 500 {
		id := fmt.Sprintf("Qm%d", i)
		got, err := proxy.AddressIn(prefix, id)
		require.NoError(t, err)
		require.NotContains(t, seen, got, "%s and %s derived the same address", seen[got], id)
		seen[got] = id
	}
}

// TestTHESUBNETRouterAnycastAddressIsNeverHandedOut — RFC 4291 §2.6.1 reserves
// it, and it is exactly what an all-zero derivation would produce.
func TestThePrefixBaseIsNotUsed(t *testing.T) {
	prefix := netip.MustParsePrefix("2001:db8::/96")

	for i := range 2000 {
		got, err := proxy.AddressIn(prefix, fmt.Sprintf("Qm%d", i))
		require.NoError(t, err)
		require.NotEqual(t, prefix.Masked().Addr(), got)
	}
}

// TestADIFFERENTPrefixMovesEverything, so an operator renumbering does not have
// to be told which proxies were where.
func TestChangingThePrefixMovesTheAddress(t *testing.T) {
	a, err := proxy.AddressIn(netip.MustParsePrefix("2001:db8:1::/64"), "QmProxy")
	require.NoError(t, err)
	b, err := proxy.AddressIn(netip.MustParsePrefix("2001:db8:2::/64"), "QmProxy")
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}

// TestV4IsRefusedRatherThanHashedInto. A /24 holds 254 addresses and a
// derivation over it collides at about twenty proxies — v4 needs a real
// allocator, and faking one with a hash would eventually hand two tenants the
// same socket.
func TestAV4PrefixIsRefused(t *testing.T) {
	_, err := proxy.AddressIn(netip.MustParsePrefix("203.0.113.0/24"), "QmProxy")
	require.ErrorIs(t, err, proxy.ErrNotV6)
}

// TestAv4MappedPrefixIsRefusedToo, because it is v4 wearing a v6 shape.
func TestAV4MappedPrefixIsRefused(t *testing.T) {
	_, err := proxy.AddressIn(netip.MustParsePrefix("::ffff:203.0.113.0/120"), "QmProxy")
	require.ErrorIs(t, err, proxy.ErrNotV6)
}

// TestTOOFEWBitsIsRefusedRatherThanSilentlyModulo'd.
func TestAPrefixWithTooFewBitsIsRefused(t *testing.T) {
	_, err := proxy.AddressIn(netip.MustParsePrefix("2001:db8::/112"), "QmProxy")
	require.ErrorIs(t, err, proxy.ErrPrefixTooSmall)
}

func TestAResourceWithNoIdHasNoAddress(t *testing.T) {
	_, err := proxy.AddressIn(p64(t), "")
	require.ErrorIs(t, err, proxy.ErrNoAddress)
}

// ── the socket a proxy answers on ────────────────────────────────────────

// TestTheSOCKETIsDerivedFromTheResourceAndTheProtocolsOwnPort. A dedicated
// address is what makes the conventional port free again: on its own name 5432
// belongs to this proxy, and `psql -h db.shop.example.com` connects with nothing
// looked up by hand.
func TestTheDerivedSocketUsesTheConventionalPort(t *testing.T) {
	s := derivingService(t, "2001:db8:1:2::/64", servable())

	on, err := s.SocketFor(t.Context(), proxy.Bind{Project: "shop", Environment: "prod", Name: "pgwire"})
	require.NoError(t, err)

	ap, err := netip.ParseAddrPort(on)
	require.NoError(t, err)
	require.Equal(t, uint16(5432), ap.Port(), "postgres, as the machine's own service table spells it")
	require.True(t, netip.MustParsePrefix("2001:db8:1:2::/64").Contains(ap.Addr()))
}

// TestTHESERVICENameDefaultsToTheResourcesOwn, which is what the vocabulary
// says an omitted one means.
func TestAnOmittedServiceUsesTheResourceName(t *testing.T) {
	res := servable()
	delete(res, "service")
	res["name"] = "redis"
	s := derivingService(t, "2001:db8::/64", res)

	on, err := s.SocketFor(t.Context(), proxy.Bind{Project: "shop", Environment: "prod", Name: "pgwire"})
	require.NoError(t, err)
	ap, err := netip.ParseAddrPort(on)
	require.NoError(t, err)
	require.Equal(t, uint16(6379), ap.Port())
}

// TestASERVICENOBODYKnowsIsRefusedRatherThanGivenANumber. The whole promise of
// this kind is that a client dials the port it already expects; a proxy on a
// port nothing conventionally uses keeps none of it.
func TestAnUnknownServiceHasNoConventionalPort(t *testing.T) {
	res := servable()
	res["service"] = "not-a-real-service-name"
	s := derivingService(t, "2001:db8::/64", res)

	_, err := s.SocketFor(t.Context(), proxy.Bind{Project: "shop", Environment: "prod", Name: "pgwire"})
	require.ErrorIs(t, err, proxy.ErrNoAddress)
}

// TestACLOUDWithNoPrefixSaysSo rather than binding something arbitrary.
func TestNoPrefixMeansNoDerivedSocket(t *testing.T) {
	s := derivingService(t, "", servable())

	_, err := s.SocketFor(t.Context(), proxy.Bind{Project: "shop", Environment: "prod", Name: "pgwire"})
	require.ErrorIs(t, err, proxy.ErrNoAddress)
}
