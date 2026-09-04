//go:build dreaming

package dream_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	statsDream "github.com/taubyte/tau/services/stats/dream"
)

// THIS IS WHAT "PARITY" MEANS FOR A V2 SERVICE, and it is why it is a test
// rather than a claim: the service running here is the same code a node runs,
// constructed the same way, over a real libp2p host and a real gossipsub topic.
// Nothing is faked but the universe.
//
// It proves the ADAPTER as much as the service. dream wants
// { Node() peer.Node; Close() error }; a v2 service takes only a host.Host and
// must never take the bundle. dream/v2 is where those two meet, and a v2
// service that could not be built this way would fail at StartWithConfig rather
// than anywhere a reader would look.
//
// NO TYPED ACCESSOR IS ADDED TO dream FOR THIS. Universe.Seer() and its
// siblings are one method per v1 service on a shared type, which is the coupling
// v2 is getting away from — a v2 service is reached the same way any service is,
// by the name it registered under.

func TestAV2ServiceRunsUnderDream(t *testing.T) {
	m, err := dream.New(t.Context())
	require.NoError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	require.NoError(t, err)

	require.NoError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			statsDream.Protocol: {},
		},
	}))

	// The service was constructed through dream/v2: a node was built, its host
	// taken out of it, the v2 constructor called with only that, and the result
	// wrapped back into the shape dream holds. A pid means all of it worked.
	pids, err := u.GetServicePids(statsDream.Protocol)
	require.NoError(t, err)
	require.NotEmpty(t, pids, "dream is running a v2 service")
}
