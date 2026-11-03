// Package service is what an instance and the services it runs agree on: a
// service's package New creates it, Start starts it on an instance's Host, from
// which it builds what it runs on, and Close stops it.
package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/ipfs/go-log/v2"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/taubyte/tau/core/kvdb"
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
	// Name is the service's own name.
	Name() string

	// Peer is the instance's peer: its host, gossip, blockstore and datastore.
	Peer() peer.Node

	// Database opens one of the service's own replicated databases, its path
	// under the service's name. It is closed with the instance.
	Database(name string) (kvdb.Instance, error)

	// Listen serves handler to other nodes over p2p/rpc on id, until the
	// instance closes.
	Listen(id protocol.ID, handler http.Handler) error

	// Handle serves handler on the instance's public listener for every request
	// pattern matches, in net/http.ServeMux syntax.
	Handle(pattern string, handler http.Handler) error

	// Peers is every node discovery says runs service, this one included when
	// it does.
	Peers(service string) []libp2ppeer.ID

	// Running is every service running on this instance, for Get.
	Running() []any

	// Secrets is the cloud's vault.
	Secrets() Vault

	// Root is the instance's own directory.
	Root() string

	Logger() log.StandardLogger

	// Config decodes the service's entry of the instance's config into into, by
	// yaml. With no entry it leaves into as it was.
	Config(into any) error
}

// Vault is what the cloud's secrets service answers, from whichever node runs it.
type Vault interface {
	Secret(ctx context.Context, project, environment, secretID string) (string, error)
	Set(ctx context.Context, project, environment, name string, value []byte) error
	Delete(ctx context.Context, project, environment, name string) error
	List(ctx context.Context, project, environment string) ([]string, error)
}

// ErrNoneRuns is a service no node is known to run.
var ErrNoneRuns = errors.New("service: no node is known to run it")

// Reach is the service named as h reaches it: the handle it provided on this
// instance when it runs here, otherwise a client dial opens to a node discovery
// names. See p2p/rpc's Reach for why the local case is no client at all.
func Reach[T any](h Host, name string, dial func(host.Host, libp2ppeer.ID) (T, error)) *rpc.Reacher[T] {
	self := h.Peer().Host()
	return rpc.Reach(self, name,
		func() (libp2ppeer.ID, error) {
			for _, id := range h.Peers(name) {
				if id != self.ID() {
					return id, nil
				}
			}
			return "", fmt.Errorf("%w: %s", ErrNoneRuns, name)
		},
		func() (T, bool) { return Get[T](h) },
		func(id libp2ppeer.ID) (T, error) { return dial(self, id) })
}

// Get is the service running on this instance that is a T: how one service reaches
// another beside it, with no hop and no name.
func Get[T any](h Host) (T, bool) {
	for _, s := range h.Running() {
		if t, ok := s.(T); ok {
			return t, true
		}
	}
	var zero T
	return zero, false
}
