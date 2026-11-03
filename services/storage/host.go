package storage

import (
	"context"
	"errors"
	"fmt"
	"io"

	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/namespace"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/placement"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/discovery"
	"github.com/taubyte/tau/services/register"
)

// Role is this service as an instance runs it.
var Role service.Service = role{}

type role struct{}

func (role) Name() string { return Descriptor.Name }

func (role) Start(ctx context.Context, h service.Host) (io.Closer, error) { return Start(ctx, h) }

// Dial is a client of the storage node at id, as the store.
func Dial(h host.Host, id libp2ppeer.ID) (Store, error) { return NewClient(h, id) }

// fleetOf reads the fleet document through whichever register answers.
type fleetOf struct{ r *rpc.Reacher[register.Fleet] }

func (f fleetOf) ResolveFleet(ctx context.Context) (pkgregister.Pointer, error) {
	reg, err := f.r.Now(ctx)
	if err != nil {
		return pkgregister.Pointer{}, err
	}
	return reg.ResolveFleet(ctx)
}

func (f fleetOf) Object(ctx context.Context, id pkgregister.ObjectID) ([]byte, error) {
	reg, err := f.r.Now(ctx)
	if err != nil {
		return nil, err
	}
	return reg.Object(ctx, id)
}

// placementSource is discovery as this service reads placement from it.
type placementSource interface {
	PlacementOf(role string) []placement.Member
}

// Start runs this service on an instance: its bytes in the peer's blockstore, its
// node-local rows under the peer's datastore, its shards and catalog as the
// instance's databases, its members from this instance's discovery and the fleet
// document from whichever register answers.
func Start(ctx context.Context, h service.Host) (io.Closer, error) {
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

	fleet := service.Reach(h, register.Descriptor.Name, register.DialFleet)

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
		WithFleet(fleetOf{fleet}),
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
