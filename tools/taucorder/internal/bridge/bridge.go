// Package bridge is taucorder's seat in a cloud's swarm: a light client peer
// that watches the fleet and names, for any v2 service, the nodes serving it.
//
// TWO VIEWS, EACH FOR WHAT IT KNOWS. libp2p identify (p2p/discovery) says which
// peers speak a protocol — a fact, learned from the peer itself. The gossiped
// v2 discovery view (services/discovery's topic) says how loaded and how fresh
// each node is — a claim, but the one every node routes on. Pick takes the
// first as the candidate set and orders it by the second.
//
// The bridge subscribes to the discovery topic and never publishes on it, so
// no node ever routes work to it.
package bridge

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	libdisc "github.com/libp2p/go-libp2p/core/discovery"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	pdisc "github.com/taubyte/tau/p2p/discovery"
	"github.com/taubyte/tau/pkg/stats"
	statsService "github.com/taubyte/tau/services/stats"
)

// PickWait is how long Pick waits, unless told otherwise, for a first peer of a
// protocol nobody has been seen serving yet.
const PickWait = 10 * time.Second

// Option adjusts a Bridge as New makes it.
type Option func(*Bridge)

// WithPickWait bounds Pick's wait for a first peer at d.
func WithPickWait(d time.Duration) Option {
	return func(b *Bridge) { b.pickWait = d }
}

// Fresh is the age past which a gossiped report no longer orders a pick.
var Fresh = statsService.Config{}.Freshness()

// Bridge is a running seat. It is safe for concurrent use.
type Bridge struct {
	host     host.Host
	routing  libdisc.Discoverer
	view     *stats.View
	pickWait time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu   sync.Mutex
	disc map[protocol.ID]*pdisc.Discovery
}

// New starts a bridge on an existing host: it subscribes to cloud's
// discovery topic on ps and folds every report into its view until Close.
// routing may be nil, in which case only connected peers are candidates.
func New(ctx context.Context, h host.Host, ps statsService.PubSub, routing libdisc.Discoverer, cloud string, opts ...Option) (*Bridge, error) {
	if h == nil {
		return nil, errors.New("bridge: no host")
	}
	view, err := stats.New(stats.Options{})
	if err != nil {
		return nil, err
	}
	bus, err := statsService.NewBus(ps, cloud)
	if err != nil {
		return nil, err
	}
	b := &Bridge{host: h, routing: routing, view: view, pickWait: PickWait, done: make(chan struct{}), disc: map[protocol.ID]*pdisc.Discovery{}}
	for _, opt := range opts {
		opt(b)
	}
	b.ctx, b.cancel = context.WithCancel(ctx)
	reports, err := bus.Subscribe(b.ctx)
	if err != nil {
		b.cancel()
		return nil, err
	}
	go b.watch(reports)
	return b, nil
}

func (b *Bridge) watch(reports <-chan []byte) {
	defer close(b.done)
	for raw := range reports {
		if r, err := statsService.Decode(raw); err == nil {
			// A refused report is the sender's problem; keep listening.
			b.view.Observe(r)
		}
	}
}

// Host is the bridge's libp2p host.
func (b *Bridge) Host() host.Host { return b.host }

// Nodes is every node the bridge has heard from, most recent report each.
func (b *Bridge) Nodes() []stats.Node { return b.view.All() }

func (b *Bridge) discovery(id protocol.ID) (*pdisc.Discovery, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if d, ok := b.disc[id]; ok {
		return d, nil
	}
	var opts []pdisc.Option
	if b.routing != nil {
		opts = append(opts, pdisc.WithRouting(b.routing))
	}
	d, err := pdisc.New(b.ctx, b.host, id, opts...)
	if err != nil {
		return nil, err
	}
	b.disc[id] = d
	return d, nil
}

// Pick names the peers speaking id, best first: ready and freshly reported
// before anything else, then by reported load, then peers the gossip has not
// described at all. It waits up to PickWait, or what WithPickWait set, for a
// first peer.
func (b *Bridge) Pick(ctx context.Context, id protocol.ID) ([]peer.ID, error) {
	d, err := b.discovery(id)
	if err != nil {
		return nil, err
	}
	known := d.Known()
	if len(known) == 0 {
		wctx, cancel := context.WithTimeout(ctx, b.pickWait)
		defer cancel()
		if _, err := d.Any(wctx); err != nil {
			return nil, err
		}
		known = d.Known()
	}
	return b.order(known), nil
}

// rank is lower for better.
func (b *Bridge) rank(pid peer.ID) (int, float64) {
	n, ok := b.view.Get(pid.String())
	switch {
	case !ok:
		return 2, 0
	case n.Ready && n.Age <= Fresh:
		return 0, n.Load
	default:
		return 1, n.Load
	}
}

func (b *Bridge) order(ids []peer.ID) []peer.ID {
	out := slices.Clone(ids)
	slices.SortStableFunc(out, func(x, y peer.ID) int {
		rx, lx := b.rank(x)
		ry, ly := b.rank(y)
		switch {
		case rx != ry:
			return rx - ry
		case lx < ly:
			return -1
		case lx > ly:
			return 1
		}
		return 0
	})
	return out
}

// Failed passes a connection failure to that protocol's discovery.
func (b *Bridge) Failed(id protocol.ID, pid peer.ID) {
	if d, err := b.discovery(id); err == nil {
		d.Failed(pid)
	}
}

// Speaks is whether the peerstore says pid serves id.
func (b *Bridge) Speaks(pid peer.ID, id protocol.ID) bool {
	ok, err := b.host.Peerstore().SupportsProtocols(pid, id)
	return err == nil && len(ok) > 0
}

// Close stops watching and drops every discovery. It does not close the host.
func (b *Bridge) Close() {
	b.cancel()
	<-b.done
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, d := range b.disc {
		d.Close()
	}
}
