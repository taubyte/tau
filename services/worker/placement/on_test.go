package placement

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/rpc"
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

func onNode(t *testing.T, running ...any) *node {
	t.Helper()
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	n := &node{Node: p, Served: service.NewServed(p.ID(), nil, rpc.Retry{For: 50 * time.Millisecond})}
	for _, r := range running {
		n.running = append(n.running, service.Running{Service: r})
	}
	return n
}

func TestWorkersAreReachedFromTheStatsViewBesideThem(t *testing.T) {
	view := &table{}
	n := onNode(t, view)
	w, err := On(n)
	require.NoError(t, err)
	t.Cleanup(func() { w.Close() })
	require.Same(t, view, w.router)
	require.Equal(t, n.ID().String(), w.self)
	require.Nil(t, w.opts.local, "no worker beside it, so every node is reached through a stream")
}

func TestWorkersWithNoStatsBesideThemAreNotPlaced(t *testing.T) {
	_, err := On(onNode(t))
	require.ErrorIs(t, err, ErrNoRouter)
}
