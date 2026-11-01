package storage

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/ipfs/boxo/blockservice"
	blockstore "github.com/ipfs/boxo/blockstore"
	offline "github.com/ipfs/boxo/exchange/offline"
	"github.com/ipfs/boxo/ipld/merkledag"
	"github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
	dssync "github.com/ipfs/go-datastore/sync"
	"github.com/multiformats/go-multihash"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/pebbleds"

	"github.com/taubyte/tau/pkg/kvdb"
)

// The in-memory double is ordered: ds.MapDatastore returns rows in Go's randomised
// map order, so a "limit 1" answer changes between runs. Store tests run against
// three real backends.

// orderedMem is an in-memory Datastore that returns rows in key order — the
// realistic model of pebble, and the double every test that is not
// specifically about a backend uses.
type orderedMem struct {
	mu   sync.Mutex
	rows map[string][]byte

	// synced records every Sync prefix, so a test can assert that a write
	// path reached the disk barrier rather than assuming it.
	synced []string

	// failPut, when set, makes the next matching Put fail. It is how a
	// mid-write crash is simulated without killing a process.
	failPut func(key string) error
}

func newOrderedMem() *orderedMem { return &orderedMem{rows: map[string][]byte{}} }

func (o *orderedMem) Get(_ context.Context, k ds.Key) ([]byte, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	v, ok := o.rows[k.String()]
	if !ok {
		return nil, ds.ErrNotFound
	}
	return append([]byte(nil), v...), nil
}

func (o *orderedMem) Has(_ context.Context, k ds.Key) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, ok := o.rows[k.String()]
	return ok, nil
}

func (o *orderedMem) Put(_ context.Context, k ds.Key, v []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.failPut != nil {
		if err := o.failPut(k.String()); err != nil {
			return err
		}
	}
	o.rows[k.String()] = append([]byte(nil), v...)
	return nil
}

func (o *orderedMem) Delete(_ context.Context, k ds.Key) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.rows, k.String())
	return nil
}

func (o *orderedMem) Sync(_ context.Context, prefix ds.Key) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.synced = append(o.synced, prefix.String())
	return nil
}

func (o *orderedMem) Query(_ context.Context, q query.Query) (query.Results, error) {
	o.mu.Lock()
	keys := make([]string, 0, len(o.rows))
	for k := range o.rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	entries := make([]query.Entry, 0, len(keys))
	for _, k := range keys {
		v := append([]byte(nil), o.rows[k]...)
		entries = append(entries, query.Entry{Key: k, Value: v, Size: len(v)})
	}
	o.mu.Unlock()
	return query.NaiveQueryApply(q, query.ResultsWithEntries(q, entries)), nil
}

var _ Datastore = (*orderedMem)(nil)

// unorderedMem wraps ds.MapDatastore, the double the ordering tests reject.
type unorderedMem struct{ inner *ds.MapDatastore }

func newUnorderedMem() *unorderedMem { return &unorderedMem{inner: ds.NewMapDatastore()} }

func (u *unorderedMem) Get(ctx context.Context, k ds.Key) ([]byte, error) { return u.inner.Get(ctx, k) }
func (u *unorderedMem) Has(ctx context.Context, k ds.Key) (bool, error)   { return u.inner.Has(ctx, k) }
func (u *unorderedMem) Put(ctx context.Context, k ds.Key, v []byte) error {
	return u.inner.Put(ctx, k, v)
}
func (u *unorderedMem) Delete(ctx context.Context, k ds.Key) error { return u.inner.Delete(ctx, k) }
func (u *unorderedMem) Sync(ctx context.Context, p ds.Key) error   { return u.inner.Sync(ctx, p) }
func (u *unorderedMem) Query(ctx context.Context, q query.Query) (query.Results, error) {
	return u.inner.Query(ctx, q)
}

var _ Datastore = (*unorderedMem)(nil)

// newPebble is a real on-disk pebble store, the production backend.
func newPebble(t *testing.T) Datastore {
	t.Helper()
	store, err := pebbleds.Open(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

// newCRDT is a real *kvdb.Datastore over pebble with a nil broadcaster (offline). Its
// physical layout puts "/v" after each key, which is why object keys are hex.
func newCRDT(t *testing.T) Datastore {
	t.Helper()
	backing := dssync.MutexWrap(ds.NewMapDatastore())
	bs := blockstore.NewBlockstore(backing)
	dag := merkledag.NewDAGService(blockservice.New(bs, offline.Exchange(bs)))

	store, err := kvdb.NewDatastore(backing, ds.NewKey("crdt/storage/s000"), dag, nil, kvdb.DefaultOptions())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

// backend is one named store to run a store test against.
type backend struct {
	name string
	open func(t *testing.T) Datastore
}

// backends is every store the layout must hold on. A test that runs on one of
// them proves less than it looks like it does.
func backends() []backend {
	return []backend{
		{"orderedMem", func(*testing.T) Datastore { return newOrderedMem() }},
		{"pebble", newPebble},
		{"kvdb", newCRDT},
	}
}

func eachBackend(t *testing.T, body func(t *testing.T, store Datastore)) {
	t.Helper()
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			body(t, b.open(t))
		})
	}
}

// addr is a valid Address for tests that do not care which one.
func addr(t *testing.T) Address {
	t.Helper()
	a, err := NewAddress("proj", "prod", "receipts", "shop")
	require.NoError(t, err)
	return a
}

// fixedClock is an injected local clock.
type fixedClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFixedClock(unixMS int64) *fixedClock { return &fixedClock{t: time.UnixMilli(unixMS)} }

func (c *fixedClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fixedClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func (c *fixedClock) set(unixMS int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = time.UnixMilli(unixMS)
}

// memBlocks is the byte plane as gc.go sees it: a set of blocks and their
// links, with no network anywhere. It is deliberately not boxo — the point of
// config.go's narrow Blocks interface is that the sweep is testable with none.
type memBlocks struct {
	mu      sync.Mutex
	present map[string][]cid.Cid
	deleted []cid.Cid

	// failDelete makes a delete fail, which is how a node dying mid-sweep is
	// simulated.
	failDelete func(c cid.Cid) error
	// failLinks makes a link read fail.
	failLinks func(c cid.Cid) error
	// failHas makes the possession check fail.
	failHas func(c cid.Cid) error
}

func newMemBlocks() *memBlocks { return &memBlocks{present: map[string][]cid.Cid{}} }

func (m *memBlocks) add(c cid.Cid, links ...cid.Cid) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.present[c.KeyString()] = links
}

func (m *memBlocks) Has(_ context.Context, c cid.Cid) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failHas != nil {
		if err := m.failHas(c); err != nil {
			return false, err
		}
	}
	_, ok := m.present[c.KeyString()]
	return ok, nil
}

func (m *memBlocks) Links(_ context.Context, c cid.Cid) ([]cid.Cid, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failLinks != nil {
		if err := m.failLinks(c); err != nil {
			return nil, err
		}
	}
	links, ok := m.present[c.KeyString()]
	if !ok {
		return nil, fmt.Errorf("memBlocks: %s is not here", c)
	}
	return links, nil
}

func (m *memBlocks) Delete(_ context.Context, c cid.Cid) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failDelete != nil {
		if err := m.failDelete(c); err != nil {
			return err
		}
	}
	delete(m.present, c.KeyString())
	m.deleted = append(m.deleted, c)
	return nil
}

func (m *memBlocks) held() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.present)
}

var _ Blocks = (*memBlocks)(nil)

// testCID makes a distinct, well-formed CIDv1 from a label.
func testCID(t *testing.T, label string) cid.Cid {
	t.Helper()
	h, err := multihash.Sum([]byte(label), multihash.SHA2_256, -1)
	require.NoError(t, err)
	return cid.NewCidV1(cid.Raw, h)
}

// staticCatalog stands in for the compiled config.
type staticCatalog struct {
	buckets []Address
	err     error
}

func (s staticCatalog) Buckets(context.Context) ([]Address, error) { return s.buckets, s.err }

var _ Catalog = staticCatalog{}

// countingOpener records what a shard set asked it to do.
type countingOpener struct {
	mu       sync.Mutex
	opened   []uint16
	closed   []uint16
	failOpen map[uint16]error
	failShut map[uint16]error
}

func newCountingOpener() *countingOpener {
	return &countingOpener{failOpen: map[uint16]error{}, failShut: map[uint16]error{}}
}

func (c *countingOpener) Open(_ context.Context, shard uint16) (Datastore, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err, bad := c.failOpen[shard]; bad {
		return nil, err
	}
	c.opened = append(c.opened, shard)
	return newOrderedMem(), nil
}

func (c *countingOpener) Close(_ context.Context, shard uint16, _ Datastore) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err, bad := c.failShut[shard]; bad {
		c.closed = append(c.closed, shard)
		return err
	}
	c.closed = append(c.closed, shard)
	return nil
}

var _ ShardOpener = (*countingOpener)(nil)

var errInjected = errors.New("injected")

// failingStore fails every door, so a test can assert that an error from the
// store reaches the caller rather than being read as "not there".
type failingStore struct{}

func (failingStore) Get(context.Context, ds.Key) ([]byte, error) { return nil, errInjected }
func (failingStore) Has(context.Context, ds.Key) (bool, error)   { return false, errInjected }
func (failingStore) Put(context.Context, ds.Key, []byte) error   { return errInjected }
func (failingStore) Delete(context.Context, ds.Key) error        { return errInjected }
func (failingStore) Sync(context.Context, ds.Key) error          { return errInjected }
func (failingStore) Query(context.Context, query.Query) (query.Results, error) {
	return nil, errInjected
}

var _ Datastore = failingStore{}

func queryPrefix(p string) query.Query { return query.Query{Prefix: p} }
