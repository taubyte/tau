package placement

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/rpc"
	statsService "github.com/taubyte/tau/services/stats"
)

// node is a service's host: a peer and what runs beside it.
type node struct {
	peer.Node
	*service.Served
	running []service.Running
}

func (n *node) Peer() peer.Node            { return n.Node }
func (n *node) Running() []service.Running { return n.running }
func (*node) Config(any) error             { return nil }
func (*node) Root() string                 { return "" }
func (*node) Cloud() string                { return "placement-test" }
func (*node) Region() string               { return "" }
func (*node) Now() time.Time               { return time.Now() }

func onNode(t *testing.T, view *table) *node {
	t.Helper()
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	n := &node{Node: p, Served: service.NewServed(p.ID(), nil, rpc.Retry{For: 50 * time.Millisecond})}
	if view != nil {
		n.Serve(statsService.Protocol, statsBeside{view})
	}
	return n
}

// statsBeside is a stats view its node serves under stats' protocol.
type statsBeside struct{ *table }

func (statsBeside) Start(context.Context, service.Host) error { return nil }
func (statsBeside) Close() error                              { return nil }

func TestWorkersAreReachedFromTheStatsViewBesideThem(t *testing.T) {
	view := &table{}
	n := onNode(t, view)
	w, err := On(n)
	require.NoError(t, err)
	t.Cleanup(func() { w.Close() })
	require.Equal(t, statsBeside{view}, w.router, "the view of the stats beside it")
	require.Equal(t, n.ID().String(), w.self)
	require.Nil(t, w.opts.local, "no worker beside it, so every node is reached through a stream")
}

func TestWorkersWithNoStatsBesideThemAreNotPlaced(t *testing.T) {
	_, err := On(onNode(t, nil))
	require.ErrorIs(t, err, ErrNoRouter)
}
