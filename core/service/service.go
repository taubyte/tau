// Package service is what an instance and the services it runs agree on: an
// instance is made from its peer and starts the services it is given, each with a
// Host, the instance's facilities, from which it builds what it runs on.
package service

import (
	"context"
	"io"
	"net/http"

	"github.com/ipfs/go-log/v2"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/peer"
)

// Service is one service an instance can run.
type Service interface {
	// Name is what an operator writes in an instance's service list, and what
	// the instance reports to discovery while it runs it.
	Name() string

	// Start brings the service up on h. What it returns is closed with the
	// instance, before what started earlier.
	Start(ctx context.Context, h Host) (io.Closer, error)
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

	// Local is the handle the service named provided on this instance, nil when
	// it runs none. Provide is this service's own, for the others here to reach
	// it without a hop.
	Local(service string) any
	Provide(handle any)

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
