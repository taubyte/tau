//go:build dreaming

package dream_test

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/mesh"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/services/register"
	registerDream "github.com/taubyte/tau/services/register/dream"

	registerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/register/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/register/v2/registerv2connect"
)

// THIS IS THE PROPERTY A MAP CANNOT SHOW. pkg/register is a state machine over
// an injected store, and its package doc says replication is the store's
// question and not the register's. That is a correct division and it is also a
// claim about something nothing in that package can test.
//
// So this is the test that pays for it: two register nodes in one cloud,
// each with its own kvdb, a publish sent over the wire to one of them, and the
// pointer read back over the wire from the OTHER. Nothing is faked but the
// cloud — real hosts, real gossipsub, real CRDT, the same code a node runs.
func TestAPublishOnOneNodeResolvesOnAnother(t *testing.T) {
	m, err := mesh.New(t.Context(), t.Name())
	require.NoError(t, err)
	defer m.Close()

	var pids []string
	for range 2 {
		node, err := registerDream.Start(m)
		require.NoError(t, err)
		pids = append(pids, node.ID().String())
	}
	require.Len(t, pids, 2, "two register nodes, so replication is a thing that can be observed")

	nodes := nodesByID(m)
	a, b := pids[0], pids[1]

	// Each node's host dials the other's, so every call below genuinely
	// crosses a stream rather than reaching into a service in this process.
	toB := dial(t, nodes[a], b)
	toA := dial(t, nodes[b], a)

	_, err = toB.Publish(t.Context(), connect.NewRequest(&registerv2.PublishRequest{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("memory: 64MB"),
	}))
	require.NoError(t, err)

	// The CRDT is eventually consistent, so the assertion is that it arrives,
	// not that it has already arrived.
	var pointer *registerv2.Pointer
	require.Eventually(t, func() bool {
		res, err := toA.Resolve(t.Context(), connect.NewRequest(&registerv2.ResolveRequest{
			Project: "shop", Environment: "prod",
		}))
		if err != nil {
			return false
		}
		pointer = res.Msg
		return true
	}, 30*time.Second, 250*time.Millisecond, "the pointer published on one node reached the other")

	require.Equal(t, uint64(1), pointer.GetRevision())

	// And the object it names came across too, which is the half that actually
	// carries the user's configuration.
	obj, err := toA.Object(t.Context(), connect.NewRequest(&registerv2.ObjectRequest{Object: pointer.GetObject()}))
	require.NoError(t, err)
	require.Equal(t, []byte("memory: 64MB"), obj.Msg.GetCompiled())
}

func nodesByID(m *mesh.Mesh) map[string]peer.Node {
	out := map[string]peer.Node{}
	for _, n := range m.Peers() {
		out[n.ID().String()] = n
	}
	return out
}

func dial(t *testing.T, from peer.Node, to string) registerv2connect.RegisterServiceClient {
	t.Helper()
	id, err := libp2ppeer.Decode(to)
	require.NoError(t, err)

	c, err := rpc.NewClient(from.Host(), id, register.Protocol)
	require.NoError(t, err)
	t.Cleanup(c.Close)

	return registerv2connect.NewRegisterServiceClient(c.HTTP(), c.URL())
}
