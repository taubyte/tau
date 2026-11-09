package clients

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/discovery"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/rpc"
	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
	"github.com/taubyte/tau/services/plm"
)

// instance is a host whose discovery names the peers it was told.
type instance struct {
	peer.Node
	*service.Served
	finding named
}

func newInstance(t *testing.T, found ...libp2ppeer.ID) *instance {
	t.Helper()
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	i := &instance{Node: p, finding: found}
	i.Served = service.NewServed(p.ID(), &i.finding, rpc.Retry{For: 50 * time.Millisecond})
	return i
}

func (i *instance) Peer() peer.Node          { return i.Node }
func (*instance) Running() []service.Running { return nil }
func (*instance) Config(any) error           { return nil }
func (*instance) Root() string               { return "" }
func (*instance) Cloud() string              { return "test" }
func (*instance) Region() string             { return "" }
func (*instance) Now() time.Time             { return time.Now() }

// named is a finder that finds these nodes for every protocol.
type named []libp2ppeer.ID

func (n *named) Find(protocol.ID) discovery.Resolver { return discovery.Static(*n...) }

func TestTheFinderNamesWhoServesAProtocolAndOthersLeavesThisNodeOut(t *testing.T) {
	h := newInstance(t)
	p := h.Node

	require.Empty(t, Discovered(h, plm.Protocol).Known(), "nothing to find with is nobody found")
	_, err := Discovered(h, plm.Protocol).Any(t.Context())
	require.ErrorIs(t, err, discovery.ErrNoPeers)

	h.finding = named{"far", "farther"}
	require.Equal(t, []libp2ppeer.ID{"far", "farther"}, Discovered(h, plm.Protocol).Known())
	h.finding = named{p.ID(), "far", "farther"}
	require.Equal(t, []libp2ppeer.ID{"far", "farther"}, Others(h, plm.Protocol).Known(), "this node does not dial itself")

	first, err := Others(h, plm.Protocol).Any(t.Context())
	require.NoError(t, err)
	require.Equal(t, libp2ppeer.ID("far"), first)
	two, err := Others(h, plm.Protocol).Peers(t.Context(), 2)
	require.NoError(t, err)
	require.Len(t, two, 2)
	Others(h, plm.Protocol).Failed("far")
}

func TestABuildNodeThatKnowsNoOtherIsToldSoAtOnce(t *testing.T) {
	h := newInstance(t)
	p := h.Node
	h.finding = named{p.ID()}

	_, err := Others(h, plm.Protocol).Peers(t.Context(), 4)
	require.ErrorIs(t, err, discovery.ErrNoPeers, "the only plm node it knows is itself")

	pool, err := NewPool(p.Host(), Others(h, plm.Protocol), plm.Protocol, 4)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	elsewhere, err := Builds(pool)
	require.NoError(t, err)
	// A LOG READ CARRIES NO DEADLINE, so this is the call that waited for a build
	// node a one-node cloud never has.
	_, err = elsewhere.Snapshot(t.Context(), "job-1", 1, 0, func(*plmv2.Job_Event) error { return nil })
	require.ErrorIs(t, err, ErrNoBackend)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
}
