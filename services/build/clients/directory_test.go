package clients

import (
	"context"
	"testing"
	"time"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
)

// instance is a host whose directory names the peers it was told.
type instance struct {
	peer.Node
	running []service.Running
}

func (i *instance) Peer() peer.Node            { return i.Node }
func (i *instance) Running() []service.Running { return i.running }
func (*instance) Config(any) error             { return nil }
func (*instance) Root() string                 { return "" }
func (*instance) Cloud() string                { return "test" }
func (*instance) Region() string               { return "" }
func (*instance) Now() time.Time               { return time.Now() }

type named []libp2ppeer.ID

func (n named) Peers(string) []libp2ppeer.ID { return n }

func TestTheDirectoryNamesWhoRunsAServiceAndOthersLeavesThisNodeOut(t *testing.T) {
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	h := &instance{Node: p}

	require.Nil(t, Discovered(h, "build").Known(), "no directory is nobody asked")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := Discovered(h, "build").Any(ctx)
	require.ErrorIs(t, err, context.Canceled, "a lookup waiting for a node ends with its context")

	h.running = []service.Running{{Name: "discovery", Service: named{p.ID(), "far", "farther"}}}
	require.Equal(t, []libp2ppeer.ID{p.ID(), "far", "farther"}, Discovered(h, "build").Known())
	require.Equal(t, []libp2ppeer.ID{"far", "farther"}, Others(h, "build").Known(), "this node does not dial itself")

	first, err := Others(h, "build").Any(t.Context())
	require.NoError(t, err)
	require.Equal(t, libp2ppeer.ID("far"), first)
	two, err := Discovered(h, "build").Peers(t.Context(), 2)
	require.NoError(t, err)
	require.Len(t, two, 2)
	Discovered(h, "build").Failed("far")
}
