package storage

import (
	"context"
	"errors"
	"fmt"
	"io"

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
	"github.com/taubyte/tau/services/cm/settings"
)

// Dial is a client of the storage node at id, as the store.
func Dial(h host.Host, id libp2ppeer.ID) (Store, error) { return NewClient(h, id) }

// closer is a close that answers nothing, as a database's does.
type closer func()

func (c closer) Close() error { c(); return nil }

// Start runs this service on an instance: its bytes in the peer's blockstore, its
// node-local rows under the peer's datastore, its shards and catalog as replicated
// databases on the peer, its members whoever discovery finds serving storage, R and K from cm.
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

	// A NODE DOES NOT START BEFORE IT HAS WHAT IT NEEDS: the region's settings from
	// cm, without which it would not know how many copies to place or wait for.
	region := settings.On(h)
	s.also = append(s.also, region)
	if _, err := region.Wait(ctx); err != nil {
		return fmt.Errorf("storage: the region's settings: %w", err)
	}
	s.region = region

	// THE STORAGE NODES ARE WHO DISCOVERY FINDS SERVING STORAGE, and this one. A
	// node that has found nobody else yet is a region of one; when discovery finds
	// the others, placement changes and the handoff moves the copies (rehome.go).
	self := p.ID().String()
	members := func() []placement.Member {
		out := []placement.Member{{ID: self, Accepts: true}}
		for _, id := range service.Serving(h, Protocol) {
			out = append(out, placement.Member{ID: id.String(), Accepts: true})
		}
		return out
	}

	// EVERY DATABASE HERE IS UNDER THIS SERVICE'S NAME, so nothing else on the
	// peer opens one.
	open := func(name string) (kvdb.Instance, error) {
		return pkgkvdb.Open(p, Descriptor.Name+"/"+name, pkgkvdb.WithLogger(logger))
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
		withMembers(members),
		withLogger(logger.Errorf),
	)
	if err != nil {
		return err
	}
	if err := s.serve(ctx); err != nil {
		return err
	}
	h.Serve(Protocol, s)
	return nil
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
