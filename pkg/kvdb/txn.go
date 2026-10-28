package kvdb

import (
	"context"
	"sort"

	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
)

// txn collects every write of one merge and commits them as one batch, so a
// crash leaves either the whole merge or none of it. A merge decides from
// what it has already decided, so reads see the pending writes first.
type txn struct {
	store  ds.Datastore
	writes map[string][]byte // nil value: deleted
	order  []string
	// sorted orders the written keys, so a scan reads only the ones under
	// its prefix: a merge scans once per key it touched.
	sorted keyIndex
	// steps counts the index nodes scans visited.
	steps int
}

func newTxn(store ds.Datastore) *txn {
	return &txn{store: store, writes: map[string][]byte{}, sorted: keyIndex{root: -1}}
}

func (t *txn) put(k ds.Key, v []byte) {
	if v == nil {
		v = []byte{}
	}
	t.set(k.String(), v)
}

func (t *txn) del(k ds.Key) {
	t.set(k.String(), nil)
}

func (t *txn) set(k string, v []byte) {
	if _, ok := t.writes[k]; !ok {
		t.order = append(t.order, k)
		t.sorted.insert(t.order)
	}
	t.writes[k] = v
}

func (t *txn) get(ctx context.Context, k ds.Key) ([]byte, error) {
	if v, ok := t.writes[k.String()]; ok {
		if v == nil {
			return nil, ds.ErrNotFound
		}
		return v, nil
	}
	return t.store.Get(ctx, k)
}

func (t *txn) has(ctx context.Context, k ds.Key) (bool, error) {
	if v, ok := t.writes[k.String()]; ok {
		return v != nil, nil
	}
	return t.store.Has(ctx, k)
}

type entry struct {
	key   string
	value []byte
}

// scan returns every entry under prefix, pending writes included, in key
// order.
func (t *txn) scan(ctx context.Context, prefix ds.Key) ([]entry, error) {
	found := map[string][]byte{}
	if err := scanStore(ctx, t.store, prefix, func(k string, v []byte) error {
		found[k] = v
		return nil
	}); err != nil {
		return nil, err
	}
	// Every key under p sorts in [p, p with its '/' raised to '0').
	p := prefix.String()
	t.steps += t.sorted.each(t.order, p+"/", p+"0", func(k string) {
		if v := t.writes[k]; v == nil {
			delete(found, k)
		} else {
			found[k] = v
		}
	})
	out := make([]entry, 0, len(found))
	for k, v := range found {
		out = append(out, entry{key: k, value: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out, nil
}

// A keyIndex is a treap over the positions of an append-only key list,
// ordered by key. Its nodes are positions, so it holds no pointers, and a
// node's priority is a hash of its position, so its shape is a function of
// the keys alone.
type keyIndex struct {
	left, right []int32
	root        int32
}

func treapPriority(n int32) uint64 {
	z := uint64(n) + 0x9e3779b97f4a7c15
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// insert adds the last key of keys, which must not already be in the index.
func (x *keyIndex) insert(keys []string) {
	n := int32(len(keys) - 1)
	x.left, x.right = append(x.left, -1), append(x.right, -1)
	l, r := x.split(keys, x.root, keys[n])
	x.root = x.join(x.join(l, n), r)
}

// split cuts the subtree at n into the keys below k and the rest.
func (x *keyIndex) split(keys []string, n int32, k string) (int32, int32) {
	if n < 0 {
		return -1, -1
	}
	if keys[n] < k {
		l, r := x.split(keys, x.right[n], k)
		x.right[n] = l
		return n, r
	}
	l, r := x.split(keys, x.left[n], k)
	x.left[n] = r
	return l, n
}

// join joins two subtrees, every key of a below every key of b.
func (x *keyIndex) join(a, b int32) int32 {
	if a < 0 {
		return b
	}
	if b < 0 {
		return a
	}
	if treapPriority(a) > treapPriority(b) {
		x.right[a] = x.join(x.right[a], b)
		return a
	}
	x.left[b] = x.join(a, x.left[b])
	return b
}

// each calls fn, in key order, with every key in [lo, hi), and returns the
// number of nodes it visited.
func (x *keyIndex) each(keys []string, lo, hi string, fn func(k string)) int {
	return x.walk(keys, x.root, lo, hi, fn)
}

func (x *keyIndex) walk(keys []string, n int32, lo, hi string, fn func(k string)) int {
	steps := 0
	for n >= 0 {
		steps++
		k := keys[n]
		if k < lo {
			n = x.right[n]
			continue
		}
		if k >= hi {
			n = x.left[n]
			continue
		}
		steps += x.walk(keys, x.left[n], lo, hi, fn)
		fn(k)
		n = x.right[n]
	}
	return steps
}

func (t *txn) commit(ctx context.Context) error {
	if len(t.writes) == 0 {
		return nil
	}
	b, err := batchOf(ctx, t.store)
	if err != nil {
		return err
	}
	for _, k := range t.order {
		v := t.writes[k]
		if v == nil {
			err = b.Delete(ctx, ds.RawKey(k))
		} else {
			err = b.Put(ctx, ds.RawKey(k), v)
		}
		if err != nil {
			return err
		}
	}
	return b.Commit(ctx)
}

// batchOf is the store's own batch when it has one, and otherwise a batch
// that applies its writes in order on commit.
func batchOf(ctx context.Context, store ds.Datastore) (ds.Batch, error) {
	if b, ok := store.(ds.Batching); ok {
		return b.Batch(ctx)
	}
	return ds.NewBasicBatch(store), nil
}

func scanStore(ctx context.Context, store ds.Datastore, prefix ds.Key, fn func(k string, v []byte) error) error {
	results, err := store.Query(ctx, query.Query{Prefix: prefix.String()})
	if err != nil {
		return err
	}
	defer results.Close()
	for {
		r, ok := results.NextSync()
		if !ok {
			return nil
		}
		if r.Error != nil {
			return r.Error
		}
		if err := fn(r.Key, r.Value); err != nil {
			return err
		}
	}
}
