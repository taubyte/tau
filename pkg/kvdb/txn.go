package kvdb

import (
	"context"
	"sort"
	"strings"

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
}

func newTxn(store ds.Datastore) *txn {
	return &txn{store: store, writes: map[string][]byte{}}
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
	p := prefix.String() + "/"
	for k, v := range t.writes {
		if !strings.HasPrefix(k, p) {
			continue
		}
		if v == nil {
			delete(found, k)
		} else {
			found[k] = v
		}
	}
	out := make([]entry, 0, len(found))
	for k, v := range found {
		out = append(out, entry{key: k, value: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out, nil
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
	for r := range results.Next() {
		if r.Error != nil {
			return r.Error
		}
		if err := fn(r.Key, r.Value); err != nil {
			return err
		}
	}
	return nil
}
