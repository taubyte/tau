//go:build dreaming

package dream_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/mesh"
	statsDream "github.com/taubyte/tau/services/stats/dream"
)

// The service runs the same code a node runs, over a real libp2p host and gossipsub
// topic, taking only a host.Host and never the peer.Node bundle.

func TestAV2ServiceRunsUnderDream(t *testing.T) {
	m, err := mesh.New(t.Context(), t.Name())
	require.NoError(t, err)
	defer m.Close()

	// A peer was built, its host taken out of it, and the v2 constructor
	// called with only that. A peer back means all of it worked.
	node, err := statsDream.Start(m)
	require.NoError(t, err)
	require.NotEmpty(t, node.ID().String(), "the mesh is running a v2 service")
}
