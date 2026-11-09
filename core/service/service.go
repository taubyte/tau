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

	// The instance answers who serves a protocol, since it knows what it runs.
	Discovery
}

// Discovery is who serves a protocol, as the node a service runs on answers it: the
// node itself first, for what it serves, then whoever p2p discovery finds.
type Discovery interface {
	// Find is the nodes serving id, this one first when it serves id.
	Find(id protocol.ID) discovery.Resolver

	// Local is the service this node serves id with, when it does: what a client of
	// id on this node uses, with no hop.
	Local(id protocol.ID) (Service, bool)

	// Serve says this node serves id with s. The first to say so is what the node
	// serves id with, so whoever composes a node can serve a protocol before its
	// own service does: a mocked version, for a test.
	Serve(id protocol.ID, s Service)

	// Retry is how every client on this node waits for a service it cannot reach
	// yet, from the node's config.
	Retry() rpc.Retry
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

// Finder finds the nodes serving a protocol over p2p: libp2p identify for the peers
// this node is connected to, then routing for the ones it has not met. It never
// reads the stats service's usage table, which dns and placement read.
type Finder interface {
	Find(id protocol.ID) discovery.Resolver
}

// Served is a node's Discovery: what it serves, and a Finder for the rest. Its zero
// value finds nothing beyond this node, which is a node with no p2p discovery, and a
// nil one finds nothing at all.
type Served struct {
	self  libp2ppeer.ID
	p2p   Finder
	retry rpc.Retry
	mu    sync.Mutex
	local map[protocol.ID]Service
}

// NewServed is the discovery of the node self, finding others with p2p, its clients
// waiting by retry.
func NewServed(self libp2ppeer.ID, p2p Finder, retry rpc.Retry) *Served {
	return &Served{self: self, p2p: p2p, retry: retry}
}

// Retry is how clients on this node wait; a nil Served's is the defaults.
func (s *Served) Retry() rpc.Retry {
	if s == nil {
		return rpc.Retry{}
	}
	return s.retry
}

func (s *Served) Serve(id protocol.ID, svc Service) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.local == nil {
		s.local = map[protocol.ID]Service{}
	}
	if _, taken := s.local[id]; !taken {
		s.local[id] = svc
	}
}

func (s *Served) Local(id protocol.ID) (Service, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.local[id]
	return v, ok
}

func (s *Served) Find(id protocol.ID) discovery.Resolver {
	var found discovery.Resolver = discovery.Static()
	if s == nil {
		return found
	}
	if s.p2p != nil {
		found = s.p2p.Find(id)
	}
	if _, ok := s.Local(id); ok && s.self != "" {
		return selfFirst{self: s.self, Resolver: found}
	}
	return found
}

// selfFirst is a node's answer for a protocol it serves itself: itself, then the
// others.
type selfFirst struct {
	discovery.Resolver
	self libp2ppeer.ID
}

func (s selfFirst) Known() []libp2ppeer.ID {
	return append([]libp2ppeer.ID{s.self}, without(s.Resolver.Known(), s.self)...)
}

func (s selfFirst) Any(context.Context) (libp2ppeer.ID, error) { return s.self, nil }

func (s selfFirst) Peers(ctx context.Context, want int) ([]libp2ppeer.ID, error) {
	out := []libp2ppeer.ID{s.self}
	if want <= 1 {
		return out, nil
	}
	if others, err := s.Resolver.Peers(ctx, want); err == nil {
		out = append(out, without(others, s.self)...)
	}
	return out[:min(len(out), want)], nil
}

func without(ids []libp2ppeer.ID, self libp2ppeer.ID) []libp2ppeer.ID {
	out := make([]libp2ppeer.ID, 0, len(ids))
	for _, id := range ids {
		if id != self {
			out = append(out, id)
		}
	}
	return out
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

// Reach is the service serving id as h reaches it: what this node serves id with
// when it does, otherwise a client dial opens to a node discovery names. See p2p/rpc's
// Reach for why the local case is no client at all.
func Reach[T any](h Host, id protocol.ID, dial func(host.Host, libp2ppeer.ID) (T, error)) *rpc.Reacher[T] {
	return reach(h, id, func() (T, bool) { return Local[T](h, id) }, dial)
}

// ReachAs is Reach for a service that serves as an S and is reached as the T as
// makes of it, such as a service whose store is what its clients mirror.
func ReachAs[S, T any](h Host, id protocol.ID, as func(S) T, dial func(host.Host, libp2ppeer.ID) (T, error)) *rpc.Reacher[T] {
	return reach(h, id, func() (T, bool) {
		s, ok := Local[S](h, id)
		if !ok {
			var zero T
			return zero, false
		}
		return as(s), true
	}, dial)
}

// Local is the service h's node serves id with, as a T, when it serves id with one.
func Local[T any](h Host, id protocol.ID) (T, bool) {
	svc, ok := h.Local(id)
	if !ok {
		var zero T
		return zero, false
	}
	t, ok := svc.(T)
	return t, ok
}

// Serving is every node discovery finds serving id, h's own node left out.
func Serving(h Host, id protocol.ID) []libp2ppeer.ID {
	found := h.Find(id).Known()
	if len(found) == 0 {
		return nil
	}
	return without(found, h.Peer().Host().ID())
}

// Found is Serving, or why it is empty.
func Found(h Host, id protocol.ID) ([]libp2ppeer.ID, error) {
	out := Serving(h, id)
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoneRuns, id)
	}
	return out, nil
}

func reach[T any](h Host, id protocol.ID, local func() (T, bool), dial func(host.Host, libp2ppeer.ID) (T, error)) *rpc.Reacher[T] {
	self := h.Peer().Host()
	return rpc.Reach(self, string(id), h.Retry(),
		func(context.Context) (libp2ppeer.ID, error) {
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
	return rpc.ReachRouted(self, string(id), h.Retry(),
		func(context.Context) (libp2ppeer.ID, error) {
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
// composes. Each service started is beside the ones started before it, and found
// there by what it serves.
type Lone struct {
	node  peer.Node
	cloud string
	*Served

	mu      sync.Mutex
	running []Running
}

// On is a Lone over p, in cloud.
func On(p peer.Node, cloud string) *Lone {
	return &Lone{node: p, cloud: cloud, Served: NewServed(p.ID(), nil, rpc.Retry{})}
}

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

func (h loneHost) Find(id protocol.ID) discovery.Resolver { return h.l.Find(id) }
func (h loneHost) Local(id protocol.ID) (Service, bool)   { return h.l.Local(id) }
func (h loneHost) Serve(id protocol.ID, svc Service)      { h.l.Serve(id, svc) }
func (h loneHost) Retry() rpc.Retry                       { return h.l.Retry() }

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
