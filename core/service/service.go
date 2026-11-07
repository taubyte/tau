// Package service is what an instance and the services it runs agree on: a
// service's package New creates it, Start starts it on an instance's Host, from
// which it builds what it runs on, and Close stops it.
package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/discovery"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/rpc"
)

// Service is one service an instance runs. Each service's own interface embeds it.
type Service interface {
	// Start brings the service up on h.
	Start(ctx context.Context, h Host) error

	// Close stops it. The instance closes its services in the reverse of the
	// order it started them.
	Close() error
}

// Host is what an instance gives each service it runs.
type Host interface {
	// Peer is the instance's peer: its host, gossip, blockstore and datastore,
	// which everything a service runs on is built from.
	Peer() peer.Node

	// Running is every service running on this instance, under the name the
	// config gave it.
	Running() []Running

	// Config decodes the service's section of the instance's config into into,
	// by yaml. With no section it leaves into as it was.
	Config(into any) error

	// Root is the instance's own directory.
	Root() string

	// Cloud is the cloud this instance belongs to, which scopes what it gossips.
	Cloud() string

	// Region is the region this instance serves, empty in a cloud with no regions.
	Region() string

	// Now is the instance's clock, so a test that moves time moves everything on it.
	Now() time.Time
}

// Running is one service running on an instance. A Name is empty for something the
// instance runs that is not a service it was configured with.
type Running struct {
	Name    string
	Service any
}

// Databases is the instance's replicated databases, one per name, shared by all
// that runs on it: two opens of one name on one peer would be two replicas over one
// store. The instance closes them.
type Databases interface {
	Database(name string) (kvdb.Instance, error)
}

// Finder finds the nodes serving a protocol: libp2p identify for the peers this node
// is connected to, then routing for the ones it has not met. It never reads the
// stats service's usage table, which dns and placement read.
type Finder interface {
	Find(id protocol.ID) discovery.Resolver
}

// ErrNoneRuns is a service no node is known to run.
var ErrNoneRuns = errors.New("service: no node is known to run it")

// Get is the service running on this instance that is a T: how one service reaches
// another beside it, with no hop and no name.
func Get[T any](h Host) (T, bool) {
	for _, r := range h.Running() {
		if t, ok := r.Service.(T); ok {
			return t, true
		}
	}
	var zero T
	return zero, false
}

// Reach is the service serving id as h reaches it: the T running on this instance
// when there is one, otherwise a client dial opens to a node the finder beside it
// names. See p2p/rpc's Reach for why the local case is no client at all.
func Reach[T any](h Host, id protocol.ID, dial func(host.Host, libp2ppeer.ID) (T, error)) *rpc.Reacher[T] {
	return reach(h, id, func() (T, bool) { return Get[T](h) }, dial)
}

// ReachAs is Reach for a service that runs as an S and is reached as the T as makes
// of it, such as a service whose store is what its clients mirror.
func ReachAs[S, T any](h Host, id protocol.ID, as func(S) T, dial func(host.Host, libp2ppeer.ID) (T, error)) *rpc.Reacher[T] {
	return reach(h, id, func() (T, bool) {
		s, ok := Get[S](h)
		if !ok {
			var zero T
			return zero, false
		}
		return as(s), true
	}, dial)
}

// Serving is every node h's finder knows serves id, h's own node left out; nil when
// nothing on h finds peers.
func Serving(h Host, id protocol.ID) []libp2ppeer.ID {
	f, ok := Get[Finder](h)
	if !ok {
		return nil
	}
	self := h.Peer().Host().ID()
	var out []libp2ppeer.ID
	for _, p := range f.Find(id).Known() {
		if p != self {
			out = append(out, p)
		}
	}
	return out
}

// Found is Serving, or why it is empty.
func Found(h Host, id protocol.ID) ([]libp2ppeer.ID, error) {
	if _, ok := Get[Finder](h); !ok {
		return nil, fmt.Errorf("%w: %s, and nothing here finds peers", ErrNoneRuns, id)
	}
	out := Serving(h, id)
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoneRuns, id)
	}
	return out, nil
}

func reach[T any](h Host, id protocol.ID, local func() (T, bool), dial func(host.Host, libp2ppeer.ID) (T, error)) *rpc.Reacher[T] {
	self := h.Peer().Host()
	return rpc.Reach(self, string(id),
		func() (libp2ppeer.ID, error) {
			found, err := Found(h, id)
			if err != nil {
				return "", err
			}
			return found[0], nil
		},
		local,
		func(p libp2ppeer.ID) (T, error) { return dial(self, p) })
}

// Routed is the service serving id at a node the finder beside h names, and here when
// it names none: for work that should leave the node handing it out. See p2p/rpc's
// ReachRouted for why that is not Reach.
func Routed[T any](h Host, id protocol.ID, here T, dial func(host.Host, libp2ppeer.ID) (T, error)) *rpc.Reacher[T] {
	self := h.Peer().Host()
	return rpc.ReachRouted(self, string(id),
		func() (libp2ppeer.ID, error) {
			found, err := Found(h, id)
			if err != nil {
				return "", err
			}
			return found[0], nil
		},
		func() (T, bool) { return here, true },
		func(p libp2ppeer.ID) (T, error) { return dial(self, p) })
}

// Lone runs services on one peer outside an instance: what a test or a dream harness
// composes. Each service started is beside the ones started before it.
type Lone struct {
	node  peer.Node
	cloud string

	mu      sync.Mutex
	running []Running
}

// On is a Lone over p, in cloud.
func On(p peer.Node, cloud string) *Lone { return &Lone{node: p, cloud: cloud} }

// Start starts s under name, with no configuration and no directory of its own.
func (l *Lone) Start(ctx context.Context, name string, s Service) error {
	if err := s.Start(ctx, loneHost{l}); err != nil {
		return err
	}
	l.mu.Lock()
	l.running = append(l.running, Running{Name: name, Service: s})
	l.mu.Unlock()
	return nil
}

type loneHost struct{ l *Lone }

func (h loneHost) Peer() peer.Node { return h.l.node }
func (h loneHost) Running() []Running {
	h.l.mu.Lock()
	defer h.l.mu.Unlock()
	return slices.Clone(h.l.running)
}
func (loneHost) Config(any) error { return nil }
func (loneHost) Root() string     { return "" }
func (h loneHost) Cloud() string  { return h.l.cloud }
func (loneHost) Region() string   { return "" }
func (loneHost) Now() time.Time   { return time.Now() }
