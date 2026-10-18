//go:build dreaming

package dream_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/mesh"
	statsDream "github.com/taubyte/tau/services/stats/dream"
)

// THIS IS WHAT "PARITY" MEANS FOR A V2 SERVICE, and it is why it is a test
// rather than a claim: the service running here is the same code a node runs,
// constructed the same way, over a real libp2p host and a real gossipsub topic.
// Nothing is faked but the universe.
//
// It proves the SPLIT as much as the service: a v2 service takes only a
// host.Host and must never take the peer.Node bundle, and Start is where the
// mesh's peer is taken apart to hand it one.

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
