package storage

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/taubyte/tau/core/kvdb"
	pkgkvdb "github.com/taubyte/tau/pkg/kvdb"
)

// Opens a shard, and the catalog, as the instance's own replicated databases: every
// holder of a shard opens one database of one name, so they replicate it between
// them.

// databaseShards opens shards over open, which is the instance's.
type databaseShards struct {
	open func(name string) (kvdb.Instance, error)

	mu  sync.Mutex
	dbs map[uint16]kvdb.Instance
}

var _ ShardOpener = (*databaseShards)(nil)

func newDatabaseShards(open func(name string) (kvdb.Instance, error)) *databaseShards {
	return &databaseShards{open: open, dbs: map[uint16]kvdb.Instance{}}
}

func (d *databaseShards) Open(_ context.Context, shard uint16) (Datastore, error) {
	db, err := d.open(fmt.Sprintf("shards/s%03d", shard))
	if err != nil {
		return nil, err
	}
	store, ok := pkgkvdb.DatastoreOf(db)
	if !ok {
		db.Close()
		return nil, fmt.Errorf("storage: shard %d did not open as a replicated store", shard)
	}
	d.mu.Lock()
	d.dbs[shard] = db
	d.mu.Unlock()
	return store, nil
}

func (d *databaseShards) Close(_ context.Context, shard uint16, _ Datastore) error {
	d.mu.Lock()
	db := d.dbs[shard]
	delete(d.dbs, shard)
	d.mu.Unlock()
	if db != nil {
		db.Close()
	}
	return nil
}

// catalogPrefix holds one row per bucket instance the fleet has written.
const catalogPrefix = "/i"

// catalog is every bucket instance the fleet has written, one row per instance with
// the same bytes from every writer, so writers never disagree about a key. Every
// storage node keeps it open, which is how a node placement newly names learns which
// shards it now holds.
type catalog struct {
	db kvdb.Instance

	mu    sync.Mutex
	known map[string]struct{}
}

var _ Catalog = (*catalog)(nil)

func newCatalog(db kvdb.Instance) *catalog {
	return &catalog{db: db, known: map[string]struct{}{}}
}

func (c *catalog) Buckets(ctx context.Context) ([]Address, error) {
	keys, err := c.db.List(ctx, catalogPrefix)
	if err != nil {
		return nil, err
	}
	out := make([]Address, 0, len(keys))
	for _, k := range keys {
		a, err := addressFromInstanceSegment(strings.TrimPrefix(k, catalogPrefix+"/"))
		if err != nil {
			return nil, fmt.Errorf("%w: catalog row %q: %w", ErrNotThisServicesRow, k, err)
		}
		out = append(out, a)
	}
	return out, nil
}

// Record notes that a has been written, once per instance per process.
func (c *catalog) Record(ctx context.Context, a Address) error {
	seg, err := instanceSegment(a)
	if err != nil {
		return err
	}
	c.mu.Lock()
	_, known := c.known[seg]
	c.mu.Unlock()
	if known {
		return nil
	}
	if err := c.db.Put(ctx, catalogPrefix+"/"+seg, []byte{1}); err != nil {
		return err
	}
	c.mu.Lock()
	c.known[seg] = struct{}{}
	c.mu.Unlock()
	return nil
}
