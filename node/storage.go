package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	ds "github.com/ipfs/go-datastore"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
)

// storage.go backs `taubyte:handler/storage` on this node — the `storage` kind
// at `type: object`.
//
// # Content-addressed underneath, named on top
//
// The BYTES go into the same store a build's artifacts do, so two objects with
// the same content are one object and neither the swarm nor the disk holds it
// twice. What this adds is a NAME: one row per object, in a replicated kvdb,
// keyed by the bucket's handle and the object's name.
//
// That split is why the two halves have different durability, and it is worth
// stating: the row replicates the way every other kvdb row does, and the bytes
// replicate the way every other content-addressed object does. An object whose
// row has reached a node and whose bytes have not is a `get` that waits on the
// DAG, which is the same shape a component that has not been fetched has.
//
// # A delete removes the name and not the bytes
//
// The bytes are content-addressed, so they may be another name's, another
// version's, or another project's. Collecting the ones nothing points at is a
// different job with a different safety argument — it has to know about every
// pointer in the cloud — and doing it from here would be a delete that can
// remove somebody else's object.
//
// # `versioning` is honoured by not collecting
//
// The vocabulary's `object/versioning` keeps historical versions. Since nothing
// collects, a replaced object's bytes stay in the store either way — so a
// bucket with versioning off is served STRONGER than it asked, which is the
// safe direction. What is missing is a way to ASK for an old version, and there
// is no verb for it: `get` takes a name and nothing else.
//
// # The size is enforced by scanning the index
//
// `size` is Required and it is a real ceiling here, not a measurement: a write
// that would take the bucket past it is refused with `full`, which is an arm of
// its own precisely so a caller can act on it. The scan is proportional to the
// number of objects in the bucket, which is the honest cost of not keeping a
// total — and a total is a counter, which is the one thing AGENTS.md says a
// kvdb key must never be: a value two nodes write differently.

// buckets is the node's Buckets: one kvdb for every bucket this node serves,
// with the handle as the key prefix.
type buckets struct {
	node *Node

	mu sync.Mutex
	db kvdb.KVDB
}

var _ wasmtier.Buckets = (*buckets)(nil)

// ErrNoObjectStore is a node asked for storage with no asset store. It is a
// composition fault: the bytes are content-addressed and only whoever holds the
// machine's DAG has somewhere to put them.
var ErrNoObjectStore = errors.New("node: storage keeps its bytes in the asset store, and this node has none")

// storageDatabase is the one database's name, for cacheDatabase's reason.
const storageDatabase = "storage"

// objectRow is what the index holds for one object.
//
// THE CID IS THE ONLY THING THAT COULD NOT BE RECOMPUTED. Size and modified
// could be read back off the bytes and the DAG, and they are here because
// `head` exists to answer without touching either.
type objectRow struct {
	CID      string `json:"cid"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}

// OpenBucket gives back the bucket a grant names.
func (b *buckets) OpenBucket(_ context.Context, grant invoke.Grant) (wasmtier.Bucket, error) {
	if b.node.opts.assets == nil {
		return nil, ErrNoObjectStore
	}

	db, err := b.store()
	if err != nil {
		return nil, err
	}

	return &bucket{
		db:     db,
		assets: b.node.opts.assets,
		// THE PREFIX ENDS IN "/" so a bucket called QmA cannot see QmAB's,
		// which is the rule pkg/cache states and keeps for the same reason.
		prefix: "objects/" + grant.Handle + "/",
		size:   bytesOf(grant.Settings[settingObjectSize]),
		now:    b.node.opts.now,
	}, nil
}

// store opens the one database, once.
func (b *buckets) store() (kvdb.KVDB, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.db != nil {
		return b.db, nil
	}
	if b.node.opts.databases == nil {
		return nil, ErrNoCacheStore
	}

	db, err := b.node.opts.databases.New(logger, storageDatabase, rebroadcastSeconds)
	if err != nil {
		return nil, fmt.Errorf("node: opening the storage database: %w", err)
	}
	b.db = db
	b.node.closers = append(b.node.closers, closerFunc(db.Close))
	return db, nil
}

// Close releases nothing of its own: the database is closed with the node and
// the bytes are the store's.
func (b *buckets) Close() error { return nil }

// The setting keys services/worker writes, which are the vocabulary's own
// attribute paths.
const (
	settingObjectSize = "size"
	settingObjectType = "type"
)

// bucket is one object store.
type bucket struct {
	db     kvdb.KVDB
	assets Assets
	prefix string

	// size is the ceiling the author declared, in bytes. Zero means the
	// compiled resource carried none, which the vocabulary does not allow —
	// and an absent one is no ceiling rather than a refusal, for the reason
	// cache.go's duration states.
	size int64

	now func() time.Time
}

var _ wasmtier.Bucket = (*bucket)(nil)

func (b *bucket) Get(ctx context.Context, key string) ([]byte, error) {
	row, err := b.row(ctx, key)
	if err != nil {
		return nil, err
	}

	content, err := b.assets.Get(ctx, row.CID)
	if err != nil {
		// THE NAME IS HERE AND THE BYTES ARE NOT. That is a fetch that has not
		// happened rather than an object that does not exist, so it is not a
		// miss: a miss would tell the caller to write it again, which would
		// mean writing over something that is on its way.
		return nil, fmt.Errorf("node: reading %s: %w", row.CID, err)
	}
	defer content.Close()

	return io.ReadAll(content)
}

func (b *bucket) Head(ctx context.Context, key string) (wasmtier.Object, error) {
	row, err := b.row(ctx, key)
	if err != nil {
		return wasmtier.Object{}, err
	}
	return wasmtier.Object{Name: key, Size: row.Size, Modified: row.Modified}, nil
}

func (b *bucket) Put(ctx context.Context, key string, value []byte) error {
	if key == "" {
		return errors.New("node: an object with no name cannot be found again")
	}

	// THE CEILING IS CHECKED BEFORE THE BYTES GO IN, and what is checked is
	// what the bucket would hold AFTER this write — with whatever this name
	// already holds taken off, because replacing a 1MB object with a 1MB
	// object does not grow anything.
	if b.size > 0 {
		held, err := b.held(ctx)
		if err != nil {
			return err
		}
		if previous, err := b.row(ctx, key); err == nil {
			held -= previous.Size
		}
		if held+int64(len(value)) > b.size {
			return fmt.Errorf("%w: %s holds %d of %d bytes and this write is %d",
				wasmtier.ErrFull, key, held, b.size, len(value))
		}
	}

	id, err := b.assets.Put(ctx, strings.NewReader(string(value)))
	if err != nil {
		return fmt.Errorf("node: storing %s: %w", key, err)
	}

	row, err := json.Marshal(objectRow{CID: id, Size: int64(len(value)), Modified: b.clock().Unix()})
	if err != nil {
		return err
	}
	return b.db.Put(ctx, b.prefix+key, row)
}

func (b *bucket) Delete(ctx context.Context, key string) error {
	if _, err := b.row(ctx, key); err != nil {
		return err
	}
	// THE ROW AND NOT THE BYTES — see the file doc.
	return b.db.Delete(ctx, b.prefix+key)
}

func (b *bucket) Entries(ctx context.Context, prefix string) ([]wasmtier.Object, error) {
	keys, err := b.db.List(ctx, b.prefix+prefix)
	if err != nil {
		return nil, err
	}

	out := make([]wasmtier.Object, 0, len(keys))
	for _, key := range keys {
		name := b.name(key)
		row, err := b.row(ctx, name)
		if err != nil {
			// A row that vanished between the listing and the read. It is not
			// this call's business to report somebody else's delete.
			continue
		}
		out = append(out, wasmtier.Object{Name: name, Size: row.Size, Modified: row.Modified})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// held is what this bucket holds now, by scanning its index. See the file doc
// for why it is a scan rather than a counter.
func (b *bucket) held(ctx context.Context) (int64, error) {
	keys, err := b.db.List(ctx, b.prefix)
	if err != nil {
		return 0, err
	}

	var total int64
	for _, key := range keys {
		row, err := b.row(ctx, b.name(key))
		if err != nil {
			continue
		}
		total += row.Size
	}
	return total, nil
}

// row reads one object's index entry.
func (b *bucket) row(ctx context.Context, key string) (objectRow, error) {
	value, err := b.db.Get(ctx, b.prefix+key)
	if err != nil {
		if errors.Is(err, ds.ErrNotFound) {
			return objectRow{}, wasmtier.ErrNotFound
		}
		return objectRow{}, err
	}

	var row objectRow
	if err := json.Unmarshal(value, &row); err != nil {
		return objectRow{}, fmt.Errorf("node: the index row for %s is not one: %w", key, err)
	}
	return row, nil
}

// name is the object's own name, with this bucket's prefix off it. A kvdb List
// answers with WHOLE keys.
func (b *bucket) name(key string) string {
	return strings.TrimPrefix(key, b.prefix)
}

func (b *bucket) clock() time.Time {
	if b.now == nil {
		return time.Now()
	}
	return b.now()
}
