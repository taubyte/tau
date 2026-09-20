package database

import (
	"testing"

	"github.com/stretchr/testify/require"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// cloud is a three-region allowance, which is the smallest one a cdn seed can
// actually choose within. Written sorted because that is how a resolved region
// list comes back — every node iterates it the same way or they disagree.
var cloud = []string{"ap", "eu", "us"}

func regionsOf(t *testing.T, p placed, allowance []string) []string {
	t.Helper()
	out, err := p.regions(at("shop", "QmSessions"), allowance)
	require.NoError(t, err)
	return out
}

// TestNoBlockIsTheAllowance, which is what almost every database writes — and
// the case that must not need a region block to be placed at all.
func TestNoBlockIsTheAllowance(t *testing.T) {
	require.Equal(t, cloud, regionsOf(t, placed{}, cloud))
	require.Equal(t, []string{anyRegion}, regionsOf(t, placed{}, unpinned),
		"an allowance naming nothing is the one unnamed bucket, not nowhere")
}

// TestPinnedIsOnlyThere. This is the data-residency line, and it is the one
// statement in the block that a fleet may never quietly widen: a database
// pinned to eu that also kept copies in us would have left the EU.
func TestPinnedIsOnlyThere(t *testing.T) {
	held := regionsOf(t, placed{policy: "pinned", name: "eu"}, cloud)
	require.Equal(t, []string{"eu"}, held, "a pin was spread across the allowance")
}

// TestFailoverPlacesInBothFromTheStart.
//
// NOT MAIN-WITH-STANDBY-AS-FALLBACK: a standby that only receives state once
// the main is gone is a standby with nothing to take over, which is the
// failure the policy is written to survive.
func TestFailoverPlacesInBothFromTheStart(t *testing.T) {
	held := regionsOf(t, placed{policy: "failover", main: "eu", standby: "us"}, cloud)
	require.Equal(t, []string{"eu", "us"}, held)
	require.Equal(t, 2*DefaultReplication, copies(held, DefaultReplication),
		"two regions at a floor of two is four nodes")
}

// TestAPinOutsideTheAllowanceIsRefused.
//
// REFUSED AND NOT NARROWED. Dropping the offending region would place the
// database somewhere its author never wrote; placing it nowhere would read as a
// fleet too small. It is a widening the compile should have caught, so it is
// named as a config error at the one moment region names are real.
func TestAPinOutsideTheAllowanceIsRefused(t *testing.T) {
	for _, p := range []placed{
		{policy: "pinned", name: "ap"},
		{policy: "failover", main: "eu", standby: "ap"},
		{policy: "cdn", name: "ap"},
	} {
		_, err := p.regions(at("shop", "QmSessions"), []string{"eu", "us"})
		require.ErrorIs(t, err, ErrOutsideAllowance, "%s was served outside its ceiling", p.policy)
		require.ErrorContains(t, err, "ap", "the refusal does not name the region")
	}
}

// TestAnEmptyAllowanceNarrowsNothing. "Absent means every region the cloud
// has" — a set this service cannot enumerate, so it cannot be intersected with
// and must not be treated as the empty set.
func TestAnEmptyAllowanceNarrowsNothing(t *testing.T) {
	held := regionsOf(t, placed{policy: "pinned", name: "eu"}, unpinned)
	require.Equal(t, []string{"eu"}, held)
}

// TestCdnSeedsWithoutCoordination.
//
// Every node computes the same seed from what it already knows, so the first
// placement of a cdn database costs no round trip and no written decision.
func TestCdnSeedsWithoutCoordination(t *testing.T) {
	cdn := placed{policy: "cdn"}

	first := regionsOf(t, cdn, cloud)
	require.Len(t, first, cdnSeed, "a cdn database seeded in one region is a pinned one")
	require.Subset(t, cloud, first, "the seed left the allowance")

	for range 20 {
		require.Equal(t, first, regionsOf(t, cdn, cloud), "two nodes would disagree about the seed")
	}
}

// TestCdnSeedsSpreadAcrossDatabases, which is the property a random pick was
// wanted for — and this one is reproducible as well.
func TestCdnSeedsSpreadAcrossDatabases(t *testing.T) {
	seen := map[string]int{}
	for _, id := range []string{"QmA", "QmB", "QmC", "QmD", "QmE", "QmF", "QmG", "QmH"} {
		out, err := placed{policy: "cdn"}.regions(at("shop", id), cloud)
		require.NoError(t, err)
		for _, region := range out {
			seen[region]++
		}
	}
	require.Len(t, seen, len(cloud), "every database seeded into the same regions")
}

// TestCdnWithAHomeStartsThere, because an author who named one has said where
// the callers are and demand has nothing to add yet.
func TestCdnWithAHomeStartsThere(t *testing.T) {
	require.Equal(t, []string{"eu"}, regionsOf(t, placed{policy: "cdn", name: "eu"}, cloud))
}

// TestASeedCannotExceedTheAllowance.
func TestASeedCannotExceedTheAllowance(t *testing.T) {
	require.Equal(t, []string{"eu"}, regionsOf(t, placed{policy: "cdn"}, []string{"eu"}))
	require.Equal(t, []string{anyRegion}, regionsOf(t, placed{policy: "cdn"}, unpinned),
		"a cdn with no allowance to choose within is the unnamed bucket")
}

// TestAShadowTakesTheAllowanceItWasWrittenInside. A shadow is a deployment over
// another environment's state, so placing that state was never its to do — the
// vocabulary gives it no `regions` field at all.
func TestAShadowTakesTheAllowanceItWasWrittenInside(t *testing.T) {
	environments := []schema.Environment{
		{Name: "preview", Regions: []string{"us"}},
		{Name: "production", Regions: []string{"eu"}, Shadows: []schema.Environment{
			{Name: "ab", Of: "production"},
		}},
	}

	require.Equal(t, []string{"eu"}, allowanceOf(environments, "ab"))
	require.Equal(t, []string{"eu"}, allowanceOf(environments, "production"))
	require.Equal(t, []string{"us"}, allowanceOf(environments, "preview"))
	require.Nil(t, allowanceOf(environments, "staging"), "an environment nobody declared has no ceiling to read")
}
