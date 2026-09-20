package database

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/placement"
)

func fleet(ids ...string) []placement.Member {
	out := make([]placement.Member, 0, len(ids))
	for _, id := range ids {
		out = append(out, placement.Member{ID: id, Accepts: true})
	}
	return out
}

func at(project, id string) address {
	return address{project: project, environment: "prod", id: id}
}

// TestADatabaseLandsOnTheReplicationFloor.
func TestADatabaseLandsOnTheReplicationFloor(t *testing.T) {
	members := fleet("n1", "n2", "n3", "n4", "n5")

	held := holders(at("shop", "QmSessions"), members, DefaultReplication)
	require.Len(t, held, DefaultReplication, "the default is two, because one is not replication")

	// AND EVERY NODE AGREES, without asking anybody. That is the whole reason
	// this is computed: a caller finds the holders with no round trip, and a
	// node being down cannot make a database unfindable.
	for _, id := range []string{"n1", "n2", "n3", "n4", "n5"} {
		require.Equal(t, held, holders(at("shop", "QmSessions"), members, DefaultReplication),
			"%s computed a different set", id)
	}

	for _, id := range held {
		require.True(t, holds(at("shop", "QmSessions"), members, DefaultReplication, id))
	}
}

// TestTwoDatabasesWithOneNameInTwoProjectsDoNotShareASet. A label is local to
// whoever wrote it; placing on it would put two tenants' data on one pair of
// machines because they picked the same word.
func TestTwoDatabasesInTwoProjectsDoNotShareASet(t *testing.T) {
	members := fleet("n1", "n2", "n3", "n4", "n5", "n6", "n7", "n8")

	shop := holders(at("shop", "QmTokens"), members, 2)
	books := holders(at("books", "QmTokens"), members, 2)
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
		was := holders(at("shop", id), before, 2)
		now := holders(at("shop", id), after, 2)

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
	members := fleet("n1", "n2", "n3", "n4")
	held := holders(at("shop", "QmSessions"), members, 2)

	declining := make([]placement.Member, len(members))
	copy(declining, members)
	for i := range declining {
		if declining[i].ID == held[0] {
			declining[i].Accepts = false
		}
	}

	now := holders(at("shop", "QmSessions"), declining, 2)
	require.NotContains(t, now, held[0])
	require.Len(t, now, 2, "the slot was left empty rather than filled")
}

// TestAFleetSmallerThanTheFloorIsHeldByAllOfIt, rather than refusing: a
// two-node cloud asked for three copies keeps two, which is what it has.
func TestAFleetSmallerThanTheFloorIsHeldByAllOfIt(t *testing.T) {
	members := fleet("n1", "n2")
	require.Len(t, holders(at("shop", "QmSessions"), members, 3), 2)
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
