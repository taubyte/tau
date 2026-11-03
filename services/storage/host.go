package storage

import (
	"context"
	"errors"
	"fmt"

	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/namespace"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/placement"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/register"
)

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
// instance's databases, its members from the discovery beside it and the fleet
// document from whichever register answers.
func (s *server) Start(ctx context.Context, h service.Host) error {
	var cfg Config
	if err := h.Config(&cfg); err != nil {
		return err
	}
	p := h.Peer()
	if p == nil {
		return errors.New("storage: no peer")
	}

	db, err := h.Database("catalog")
	if err != nil {
		return fmt.Errorf("storage: opening the catalog: %w", err)
	}

	fleet := service.Reach(h, register.Descriptor.Name, register.DialFleet)
	s.also = append(s.also, fleet)

	err = s.assemble(p.Host(), cfg,
		withBytes(peerBytes{Node: p}),
		withLocal(namespace.Wrap(p.Store(), ds.NewKey("storage/local"))),
		withShards(newDatabaseShards(h.Database)),
		withCatalog(newCatalog(db)),
		// A node running no discovery cannot tell who else holds bytes and places
		// everything on itself, as the database service does.
		withMembers(func() []placement.Member {
			if d, ok := service.Get[placementSource](h); ok {
				return d.PlacementOf(Descriptor.Name)
			}
			return nil
		}),
		withFleet(fleetOf{fleet}),
		withLogger(h.Logger().Errorf),
	)
	if err != nil {
		return err
	}
	return s.serve(ctx)
}
