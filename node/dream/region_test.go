package dream_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
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
	m, err := dream.New(t.Context())
	require.NoError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	require.NoError(t, err)

	require.NoError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			// FOUR NODES, so a spread is observable rather than asserted about one.
			nodeDream.Protocol: {Others: func() map[string]int {
				bag := nodeDream.Services(node.Register, node.Stats).In("eu", "us")
				bag["copies"] = 4
				return bag
			}()},
		},
	}))

	pids, err := u.GetServicePids(nodeDream.Protocol)
	require.NoError(t, err)
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

	// AND IT IS THE SAME EVERY RUN. The assignment is HRW over the peer id, so
	// a universe that failed can be read rather than re-rolled.
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
	m, err := dream.New(t.Context())
	require.NoError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	require.NoError(t, err)

	require.NoError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			nodeDream.Protocol: {Others: nodeDream.Services(node.Register, node.Stats)},
		},
	}))

	pids, err := u.GetServicePids(nodeDream.Protocol)
	require.NoError(t, err)
	require.NotEmpty(t, pids)

	n, running := nodeDream.Node(pids[0])
	require.True(t, running)
	require.Empty(t, n.Region())
}
