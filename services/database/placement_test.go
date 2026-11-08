package database

import (
	"testing"

	"github.com/taubyte/tau/services/cm/settings"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/placement"
)

func members(ids ...string) []placement.Member {
	out := make([]placement.Member, 0, len(ids))
	for _, id := range ids {
		out = append(out, placement.Member{ID: id, Accepts: true})
	}
	return out
}

func at(project, id string) address {
	return address{project: project, environment: "prod", id: id}
}

func TestADatabaseLandsOnTheReplicationFloor(t *testing.T) {
	members := members("n1", "n2", "n3", "n4", "n5")

	held := holders(at("shop", "QmSessions"), members, settings.Defaults().Replication)
	require.Len(t, held, settings.Defaults().Replication, "the default is two, because one is not replication")

	// Every node agrees without asking anybody.
	for _, id := range []string{"n1", "n2", "n3", "n4", "n5"} {
		require.Equal(t, held, holders(at("shop", "QmSessions"), members, settings.Defaults().Replication),
			"%s computed a different set", id)
	}

	for _, id := range held {
		require.True(t, holds(at("shop", "QmSessions"), members, settings.Defaults().Replication, id))
	}
}

func TestTwoDatabasesInTwoProjectsDoNotShareASet(t *testing.T) {
	members := members("n1", "n2", "n3", "n4", "n5", "n6", "n7", "n8")

	shop := holders(at("shop", "QmTokens"), members, 2)
	books := holders(at("books", "QmTokens"), members, 2)
	require.NotEqual(t, shop, books)
}

func TestANodeGoingAwayMovesOnlyWhatItHeld(t *testing.T) {
	before := members("n1", "n2", "n3", "n4", "n5")
	after := members("n1", "n2", "n3", "n4")

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

func TestANodeThatDeclinesIsSkipped(t *testing.T) {
	all := members("n1", "n2", "n3", "n4")
	held := holders(at("shop", "QmSessions"), members("n1", "n2", "n3", "n4"), 2)

	declining := make([]placement.Member, len(all))
	copy(declining, all)
	for i := range declining {
		if declining[i].ID == held[0] {
			declining[i].Accepts = false
		}
	}

	now := holders(at("shop", "QmSessions"), declining, 2)
	require.NotContains(t, now, held[0])
	require.Len(t, now, 2, "the slot was left empty rather than filled")
}

func TestAFleetSmallerThanTheFloorIsHeldByAllOfIt(t *testing.T) {
	members := members("n1", "n2")
	require.Len(t, holders(at("shop", "QmSessions"), members, 3), 2)
}

func contains(ids []string, id string) bool {
	for _, held := range ids {
		if held == id {
			return true
		}
	}
	return false
}
