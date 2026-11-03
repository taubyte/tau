// Package service is what an instance and the services it runs agree on: a
// service's package New creates it, Start starts it on an instance's Host, from
// which it builds what it runs on, and Close stops it.
package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

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
}

// Running is one service running on an instance. A Name is empty for something the
// instance runs that is not a service it was configured with.
type Running struct {
	Name    string
	Service any
}

// Directory says which nodes run a service: what discovery answers.
type Directory interface {
	Peers(service string) []libp2ppeer.ID
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

// Reach is the service named as h reaches it: the T running on this instance when
// there is one, otherwise a client dial opens to a node the directory beside it
// names. See p2p/rpc's Reach for why the local case is no client at all.
func Reach[T any](h Host, name string, dial func(host.Host, libp2ppeer.ID) (T, error)) *rpc.Reacher[T] {
	self := h.Peer().Host()
	return rpc.Reach(self, name,
		func() (libp2ppeer.ID, error) {
			if d, ok := Get[Directory](h); ok {
				for _, id := range d.Peers(name) {
					if id != self.ID() {
						return id, nil
					}
				}
			}
			return "", fmt.Errorf("%w: %s", ErrNoneRuns, name)
		},
		func() (T, bool) { return Get[T](h) },
		func(id libp2ppeer.ID) (T, error) { return dial(self, id) })
}
