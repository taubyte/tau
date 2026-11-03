package storage

import (
	"context"
	"errors"
	"fmt"
	"io"

	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/namespace"
	"github.com/ipfs/go-log/v2"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/placement"
	"github.com/taubyte/tau/services/discovery"
	"github.com/taubyte/tau/services/register"
)

// Host is what an instance gives this service.
type Host interface {
	// Peer is the instance's own peer: its blockstore holds the bytes and its
	// datastore the rows that never leave this node.
	Peer() peer.Node

	// Database opens one of this service's replicated databases, closed with
	// the instance.
	Database(name string) (kvdb.Instance, error)

	// Peers is every node discovery says runs service.
	Peers(service string) []libp2ppeer.ID

	// Local is the handle service provided on this instance, nil when it runs
	// none; Provide is this service's own.
	Local(service string) any
	Provide(handle any)

	Logger() log.StandardLogger

	// Config decodes this service's entry of the instance's config.
	Config(into any) error
}

// Role is this service as an instance runs it.
var Role service.Service = role{}

type role struct{}

func (role) Name() string { return Descriptor.Name }

func (role) Start(ctx context.Context, h service.Host) (io.Closer, error) { return Start(ctx, h) }

// placementSource is discovery as this service reads placement from it.
type placementSource interface {
	PlacementOf(role string) []placement.Member
}

// Start runs this service on an instance: its bytes in the peer's blockstore, its
// node-local rows under the peer's datastore, its shards and catalog as the
// instance's databases, its members from this instance's discovery and the fleet
// document from whichever register answers.
func Start(ctx context.Context, h Host) (io.Closer, error) {
	var cfg Config
	if err := h.Config(&cfg); err != nil {
		return nil, err
	}
	p := h.Peer()
	if p == nil {
		return nil, errors.New("storage: no peer")
	}

	db, err := h.Database("catalog")
	if err != nil {
		return nil, fmt.Errorf("storage: opening the catalog: %w", err)
	}
	logger := h.Logger()

	fleet := register.NewReach(p.Host(),
		func() *register.Service {
			s, _ := h.Local(register.Descriptor.Name).(*register.Service)
			return s
		},
		func() []libp2ppeer.ID { return h.Peers(register.Descriptor.Name) })

	svc, err := New(p.Host(), cfg,
		WithBytes(peerBytes{Node: p}),
		WithLocal(namespace.Wrap(p.Store(), ds.NewKey("storage/local"))),
		WithShards(newDatabaseShards(h.Database)),
		WithCatalog(newCatalog(db)),
		// A node running no discovery cannot tell who else holds bytes and places
		// everything on itself, as the database service does.
		WithMembers(func() []placement.Member {
			if d, ok := h.Local(discovery.Descriptor.Name).(placementSource); ok {
				return d.PlacementOf(Descriptor.Name)
			}
			return nil
		}),
		WithFleet(fleet),
		WithLogger(logger.Errorf),
	)
	if err != nil {
		fleet.Close()
		return nil, err
	}
	if err := svc.Start(ctx); err != nil {
		fleet.Close()
		return nil, err
	}
	h.Provide(svc)
	return closers{svc, fleet}, nil
}

type closers []io.Closer

func (c closers) Close() error {
	var errs []error
	for _, closer := range c {
		errs = append(errs, closer.Close())
	}
	return errors.Join(errs...)
}
