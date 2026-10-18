package dream_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
	nodeDream "github.com/taubyte/tau/node/dream"
)

// TestAUniverseIsAFleetWithRegIONS.
//
// A universe expresses ONE node shape — `copies: 4` is four nodes from one
// config — so a fleet spread over two regions cannot be written as two configs.
// The universe declares the regions and each node takes one, by the same
// rendezvous every placement in this system uses.
//
// This is the harness everything downstream of placement needs: without it a
// fleet is one unnamed region, which is exactly the shape that hid the sentinel
// bug in services/database.
func TestAUniverseSpreadsItsNodesAcrossRegions(t *testing.T) {
	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		// FOUR NODES, so a spread is observable rather than asserted about one.
		Services: []string{node.Register, node.Discovery},
		Regions:  []string{"eu", "us"},
		Copies:   4,
	})
	require.NoError(t, err)
	defer u.Close()

	pids := u.Pids()
	require.NotEmpty(t, pids)

	require.Len(t, pids, 4)

	seen := map[string]int{}
	for _, id := range pids {
		n, running := nodeDream.Node(id)
		require.True(t, running)
		require.Contains(t, []string{"eu", "us"}, n.Region(),
			"a node landed outside every region the universe declared")
		seen[n.Region()]++
	}

	require.Len(t, seen, 2, "every node landed in one region: %v", seen)

	// AND IT IS BALANCED, which is the claim the old assignment could not make.
	// It was HRW over the peer id: an independent coin flip per node, so four
	// nodes over two regions landed together one run in eight and this test was
	// a 12.5% flake. Round-robin over the copy index spreads by construction,
	// so two-and-two is not a probable outcome here, it is the only one.
	require.Equal(t, map[string]int{"eu": 2, "us": 2}, seen)

	// AND IT IS THE SAME EVERY RUN, which the peer id could never give: dream
	// mints a fresh one per node per run, so an assignment derived from it was
	// re-rolled every time. The copy index and the sorted region list are both
	// the same on the next run, so a universe that failed can be read rather
	// than re-rolled.
	for _, id := range pids {
		n, _ := nodeDream.Node(id)
		was := n.Region()
		for range 5 {
			again, _ := nodeDream.Node(id)
			require.Equal(t, was, again.Region())
		}
	}
}

// TestANodeInNoRegionIsTheDefault, which is every test that is not about
// placement — and what a single-region cloud is.
func TestAUniverseThatNamesNoRegionHasNone(t *testing.T) {
	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Services: []string{node.Register, node.Discovery},
	})
	require.NoError(t, err)
	defer u.Close()

	pids := u.Pids()
	require.NotEmpty(t, pids)

	n, running := nodeDream.Node(pids[0])
	require.True(t, running)
	require.Empty(t, n.Region())
}
