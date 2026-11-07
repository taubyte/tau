package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/namespace"
	logging "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/rpc"
	pkgkvdb "github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/pkg/placement"
	"github.com/taubyte/tau/services/register"
)

// Dial is a client of the storage node at id, as the store.
func Dial(h host.Host, id libp2ppeer.ID) (Store, error) { return NewClient(h, id) }

// retentionPoll is how often a database rereads the cloud's retention.
const retentionPoll = time.Minute

// closer is a close that answers nothing, as a database's does.
type closer func()

func (c closer) Close() error { c(); return nil }

// placementSource is discovery as this service reads placement from it.
type placementSource interface {
	PlacementOf(role string) []placement.Member
}

// Start runs this service on an instance: its bytes in the peer's blockstore, its
// node-local rows under the peer's datastore, its shards and catalog as replicated
// databases on the peer, its members from the discovery beside it and the fleet
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

	logger := logging.Logger("tau.storage")
	fleet := register.ViewsOn(h)
	s.also = append(s.also, fleet)

	// EVERY DATABASE HERE IS UNDER THIS SERVICE'S NAME, so nothing else on the
	// peer opens one, and each follows the cloud's retention itself.
	open := func(name string) (kvdb.Instance, error) {
		return pkgkvdb.Open(p, Descriptor.Name+"/"+name,
			pkgkvdb.WithLogger(logger),
			pkgkvdb.WithRetentionFrom(func(ctx context.Context) (time.Duration, error) {
				return register.Retention(ctx, fleet)
			}, retentionPoll))
	}
	db, err := open("catalog")
	if err != nil {
		return fmt.Errorf("storage: opening the catalog: %w", err)
	}
	s.also = append(s.also, closer(db.Close))

	err = s.assemble(p.Host(), cfg,
		withBytes(peerBytes{Node: p}),
		withLocal(namespace.Wrap(p.Store(), ds.NewKey("storage/local"))),
		withShards(newDatabaseShards(open)),
		withCatalog(newCatalog(db)),
		// A node running no discovery cannot tell who else holds bytes and places
		// everything on itself, as the database service does.
		withMembers(func() []placement.Member {
			if d, ok := service.Get[placementSource](h); ok {
				return d.PlacementOf(Descriptor.Name)
			}
			return nil
		}),
		withFleet(fleet),
		withLogger(logger.Errorf),
	)
	if err != nil {
		return err
	}
	return s.serve(ctx)
}

// Reached is the storage service as the services of an instance reach it: the one
// beside them, else a node discovery names.
type Reached struct{ reach *rpc.Reacher[Store] }

var _ Store = (*Reached)(nil)

// StoreOn is the storage service as services on h reach it.
func StoreOn(h service.Host) *Reached {
	return &Reached{reach: service.Reach(h, Protocol, Dial)}
}

func (r *Reached) Put(ctx context.Context, at Address, name string, o Object, content io.Reader) (Version, error) {
	s, err := r.reach.Now(ctx)
	if err != nil {
		return Version{}, err
	}
	return s.Put(ctx, at, name, o, content)
}

func (r *Reached) Current(ctx context.Context, at Address, name string) (Version, error) {
	s, err := r.reach.Now(ctx)
	if err != nil {
		return Version{}, err
	}
	return s.Current(ctx, at, name)
}

func (r *Reached) Versions(ctx context.Context, at Address, name string, limit int) ([]Version, error) {
	s, err := r.reach.Now(ctx)
	if err != nil {
		return nil, err
	}
	return s.Versions(ctx, at, name, limit)
}

func (r *Reached) List(ctx context.Context, at Address, prefix, after string, limit int) ([]Version, error) {
	s, err := r.reach.Now(ctx)
	if err != nil {
		return nil, err
	}
	return s.List(ctx, at, prefix, after, limit)
}

func (r *Reached) Delete(ctx context.Context, at Address, name string) (Version, error) {
	s, err := r.reach.Now(ctx)
	if err != nil {
		return Version{}, err
	}
	return s.Delete(ctx, at, name)
}

func (r *Reached) Read(ctx context.Context, root string) (io.ReadCloser, error) {
	s, err := r.reach.Now(ctx)
	if err != nil {
		return nil, err
	}
	return s.Read(ctx, root)
}

// Close releases the stream to a storage node elsewhere, if one was opened.
func (r *Reached) Close() error { return r.reach.Close() }
