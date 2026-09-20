package database

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/placement"
)

// fleet is nodes in one unnamed region, for the cases regions do not decide.
func fleet(ids ...string) map[string][]placement.Member {
	return map[string][]placement.Member{anyRegion: members(ids...)}
}

func members(ids ...string) []placement.Member {
	out := make([]placement.Member, 0, len(ids))
	for _, id := range ids {
		out = append(out, placement.Member{ID: id, Accepts: true})
	}
	return out
}

// unpinned is an environment that named no regions: the cloud decides.
var unpinned []string

func at(project, id string) address {
	return address{project: project, environment: "prod", id: id}
}

// TestADatabaseLandsOnTheReplicationFloor.
func TestADatabaseLandsOnTheReplicationFloor(t *testing.T) {
	members := fleet("n1", "n2", "n3", "n4", "n5")

	held := holders(at("shop", "QmSessions"), members, unpinned, DefaultReplication)[anyRegion]
	require.Len(t, held, DefaultReplication, "the default is two, because one is not replication")

	// AND EVERY NODE AGREES, without asking anybody. That is the whole reason
	// this is computed: a caller finds the holders with no round trip, and a
	// node being down cannot make a database unfindable.
	for _, id := range []string{"n1", "n2", "n3", "n4", "n5"} {
		require.Equal(t, held, holders(at("shop", "QmSessions"), members, unpinned, DefaultReplication)[anyRegion],
			"%s computed a different set", id)
	}

	for _, id := range held {
		require.True(t, holds(at("shop", "QmSessions"), members, unpinned, DefaultReplication, id, anyRegion))
	}
}

// TestTwoDatabasesWithOneNameInTwoProjectsDoNotShareASet. A label is local to
// whoever wrote it; placing on it would put two tenants' data on one pair of
// machines because they picked the same word.
func TestTwoDatabasesInTwoProjectsDoNotShareASet(t *testing.T) {
	members := fleet("n1", "n2", "n3", "n4", "n5", "n6", "n7", "n8")

	shop := holders(at("shop", "QmTokens"), members, unpinned, 2)[anyRegion]
	books := holders(at("books", "QmTokens"), members, unpinned, 2)[anyRegion]
	require.NotEqual(t, shop, books)
}

// TestANodeGoingAwayMovesOnlyWhatItHeld.
//
// This is the property that makes a fleet survivable: when a member leaves,
// only the databases it held move, and each moves exactly one slot — the
// departed node is replaced by the next in the same ranking and the other
// holders do not budge. A naive hash-and-mod reshuffles the whole key space on
// every restart, which is a fleet-wide data movement storm.
func TestANodeGoingAwayMovesOnlyWhatItHeld(t *testing.T) {
	before := fleet("n1", "n2", "n3", "n4", "n5")
	after := fleet("n1", "n2", "n3", "n4")

	var moved, untouched int
	for _, id := range []string{"QmA", "QmB", "QmC", "QmD", "QmE", "QmF", "QmG", "QmH"} {
		was := holders(at("shop", id), before, unpinned, 2)[anyRegion]
		now := holders(at("shop", id), after, unpinned, 2)[anyRegion]

		if contains(was, "n5") {
			moved++
			require.Len(t, now, 2, "%s lost a replica and did not get another", id)
			require.NotContains(t, now, "n5")
		} else {
			untouched++
			require.Equal(t, was, now, "%s moved although the node it lost was not its own", id)
		}
	}
	require.NotZero(t, moved, "the fixture never exercised a loss")
	require.NotZero(t, untouched, "the fixture never exercised a non-loss")
}

// TestANodeThatDeclinesIsSkipped, which is the only thing a member gets to say
// about itself: a node can lie itself OUT of a set and never into one.
func TestANodeThatDeclinesIsSkipped(t *testing.T) {
	all := members("n1", "n2", "n3", "n4")
	held := holders(at("shop", "QmSessions"), fleet("n1", "n2", "n3", "n4"), unpinned, 2)[anyRegion]

	declining := make([]placement.Member, len(all))
	copy(declining, all)
	for i := range declining {
		if declining[i].ID == held[0] {
			declining[i].Accepts = false
		}
	}

	now := holders(at("shop", "QmSessions"), map[string][]placement.Member{anyRegion: declining}, unpinned, 2)[anyRegion]
	require.NotContains(t, now, held[0])
	require.Len(t, now, 2, "the slot was left empty rather than filled")
}

// TestAFleetSmallerThanTheFloorIsHeldByAllOfIt, rather than refusing: a
// two-node cloud asked for three copies keeps two, which is what it has.
func TestAFleetSmallerThanTheFloorIsHeldByAllOfIt(t *testing.T) {
	members := fleet("n1", "n2")
	require.Len(t, holders(at("shop", "QmSessions"), members, unpinned, 3)[anyRegion], 2)
}

// TestTheReplicationFloorIsTwoUnlessAnOperatorSaysOtherwise.
func TestTheReplicationFloorIsTwoUnlessAnOperatorSaysOtherwise(t *testing.T) {
	filled, err := Config{}.withDefaults()
	require.NoError(t, err)
	require.Equal(t, 2, filled.Replication)

	filled, err = Config{Replication: 3}.withDefaults()
	require.NoError(t, err)
	require.Equal(t, 3, filled.Replication)

	_, err = Config{Replication: -1}.withDefaults()
	require.ErrorContains(t, err, "a database nobody holds is not kept")
}

func contains(ids []string, id string) bool {
	for _, held := range ids {
		if held == id {
			return true
		}
	}
	return false
}

// TestAPinnedRegionGetsTheFloorOfItsOwn.
//
// An environment declares "Regions its state is kept in", and that is a pin
// rather than a permission: a database pinned to eu and us has copies in BOTH,
// and the operator's floor is how many in EACH. Two regions at a floor of two
// is four nodes — a fleet that lost a region would otherwise lose the database,
// which is the failure the pin exists to survive.
func TestAPinnedRegionGetsTheFloorOfItsOwn(t *testing.T) {
	byRegion := map[string][]placement.Member{
		"eu": members("eu1", "eu2", "eu3"),
		"us": members("us1", "us2", "us3"),
	}
	pinned := []string{"eu", "us"}

	held := holders(at("shop", "QmSessions"), byRegion, pinned, 2)
	require.Len(t, held["eu"], 2)
	require.Len(t, held["us"], 2)
	require.Equal(t, 4, copies(pinned, 2), "two regions at a floor of two is four nodes")

	// AND NEITHER REGION'S SET LEAKS INTO THE OTHER'S, which is the residency
	// line: a row pinned to eu is never on a machine in us.
	for _, id := range held["eu"] {
		require.Contains(t, []string{"eu1", "eu2", "eu3"}, id)
	}
	for _, id := range held["us"] {
		require.Contains(t, []string{"us1", "us2", "us3"}, id)
	}
}

// TestLosingANodeInOneRegionDoesNotMoveAnother. Each region's placement is its
// own computation, so an outage in eu costs eu one replica move and touches
// nothing in us.
func TestLosingANodeInOneRegionDoesNotMoveAnother(t *testing.T) {
	pinned := []string{"eu", "us"}
	before := map[string][]placement.Member{
		"eu": members("eu1", "eu2", "eu3"),
		"us": members("us1", "us2", "us3"),
	}

	was := holders(at("shop", "QmSessions"), before, pinned, 2)

	after := map[string][]placement.Member{
		"eu": members("eu1", "eu3"),
		"us": members("us1", "us2", "us3"),
	}
	now := holders(at("shop", "QmSessions"), after, pinned, 2)

	require.Equal(t, was["us"], now["us"], "us moved because eu lost a node")
	require.Len(t, now["eu"], 2, "eu did not restore its floor")
	require.NotContains(t, now["eu"], "eu2")
}

// TestANodeOnlyCompetesInItsOwnRegion, which is the residency line holding at
// the one place it has to: a us node is never a candidate for an eu database,
// and never asks.
func TestANodeOnlyCompetesInItsOwnRegion(t *testing.T) {
	byRegion := map[string][]placement.Member{
		"eu": members("eu1", "eu2"),
		"us": members("us1", "us2"),
	}
	pinned := []string{"eu"}

	require.True(t, holds(at("shop", "QmSessions"), byRegion, pinned, 2, "eu1", "eu"))
	require.False(t, holds(at("shop", "QmSessions"), byRegion, pinned, 2, "us1", "us"),
		"a node outside every pinned region held a copy")
}

// TestAPinnedRegionNoNodeServesComesBackEmpty, rather than being filled from
// somewhere else. A database that cannot be kept where it was pinned is a
// refusal an operator has to see, not a copy quietly placed outside its
// residency line.
func TestAPinnedRegionNoNodeServesComesBackEmpty(t *testing.T) {
	byRegion := map[string][]placement.Member{"eu": members("eu1", "eu2")}

	held := holders(at("shop", "QmSessions"), byRegion, []string{"eu", "ap"}, 2)
	require.Len(t, held["eu"], 2)
	require.Empty(t, held["ap"], "a region with no members was filled from another")
}

// TestAHoleInThePinnedListIsNotAnywhere. An empty name among named regions is a
// broken declaration; reading it as "the cloud decides" would place a copy
// outside every region the author wrote.
func TestAHoleInThePinnedListIsNotAnywhere(t *testing.T) {
	require.Equal(t, []string{"eu"}, placementRegions([]string{"eu", ""}))
	require.Equal(t, []string{anyRegion}, placementRegions(nil))
	require.Equal(t, []string{"eu", "us"}, placementRegions([]string{"us", "eu", "us"}),
		"a region named twice is one region, and the order is stable")
}
