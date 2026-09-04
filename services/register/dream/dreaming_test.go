//go:build dreaming

package dream_test

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/services/register"
	registerDream "github.com/taubyte/tau/services/register/dream"

	registerv1 "github.com/taubyte/tau/pkg/proto/gen/tau/register/v1"
	"github.com/taubyte/tau/pkg/proto/gen/tau/register/v1/registerv1connect"
)

// THIS IS THE PROPERTY A MAP CANNOT SHOW. pkg/register is a state machine over
// an injected store, and its package doc says replication is the store's
// question and not the register's. That is a correct division and it is also a
// claim about something nothing in that package can test.
//
// So this is the test that pays for it: two register nodes in one universe,
// each with its own kvdb, a publish sent over the wire to one of them, and the
// pointer read back over the wire from the OTHER. Nothing is faked but the
// universe — real hosts, real gossipsub, real CRDT, the same code a node runs.
func TestAPublishOnOneNodeResolvesOnAnother(t *testing.T) {
	m, err := dream.New(t.Context())
	require.NoError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	require.NoError(t, err)

	require.NoError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			registerDream.Protocol: {Others: map[string]int{"copies": 2}},
		},
	}))

	pids, err := u.GetServicePids(registerDream.Protocol)
	require.NoError(t, err)
	require.Len(t, pids, 2, "two register nodes, so replication is a thing that can be observed")

	nodes := nodesByID(u)
	a, b := pids[0], pids[1]

	// Each node's host dials the other's, so every call below genuinely
	// crosses a stream rather than reaching into a service in this process.
	toB := dial(t, nodes[a], b)
	toA := dial(t, nodes[b], a)

	_, err = toB.Publish(t.Context(), connect.NewRequest(&registerv1.PublishRequest{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("memory: 64MB"),
	}))
	require.NoError(t, err)

	// The CRDT is eventually consistent, so the assertion is that it arrives,
	// not that it has already arrived.
	var pointer *registerv1.Pointer
	require.Eventually(t, func() bool {
		res, err := toA.Resolve(t.Context(), connect.NewRequest(&registerv1.ResolveRequest{
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
	obj, err := toA.Object(t.Context(), connect.NewRequest(&registerv1.ObjectRequest{Object: pointer.GetObject()}))
	require.NoError(t, err)
	require.Equal(t, []byte("memory: 64MB"), obj.Msg.GetCompiled())
}

func nodesByID(u *dream.Universe) map[string]peer.Node {
	out := map[string]peer.Node{}
	for _, n := range u.Peers() {
		out[n.ID().String()] = n
	}
	return out
}

func dial(t *testing.T, from peer.Node, to string) registerv1connect.RegisterServiceClient {
	t.Helper()
	id, err := libp2ppeer.Decode(to)
	require.NoError(t, err)

	c, err := rpc.NewClient(from.Peer(), id, register.Protocol)
	require.NoError(t, err)
	t.Cleanup(c.Close)

	return registerv1connect.NewRegisterServiceClient(c.HTTP(), c.URL())
}
