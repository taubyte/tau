package kvdb

import (
	"context"
	"errors"

	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	"github.com/taubyte/tau/core/kvdb"
)

var _ kvdb.Compactor = (*kvDatabase)(nil)

func (kvd *kvDatabase) Compact(ctx context.Context) (int, error) {
	return kvd.datastore.Compact(ctx)
}

func (kvd *kvDatabase) Get(ctx context.Context, key string) ([]byte, error) {
	k := ds.NewKey(key)
	return kvd.datastore.Get(ctx, k)
}

func (kvd *kvDatabase) Put(ctx context.Context, key string, v []byte) error {
	if key == "" {
		return errors.New("key cannot be empty")
	}

	k := ds.NewKey(key)
	return kvd.datastore.Put(ctx, k, v)
}

func (kvd *kvDatabase) Delete(ctx context.Context, key string) error {
	k := ds.NewKey(key)
	return kvd.datastore.Delete(ctx, k)
}

// List is every key under prefix. A scan that fails partway is an error, never the
// keys read before it: a partial list read as complete undercounts every quota,
// claim and sweep built on it.
func (kvd *kvDatabase) List(ctx context.Context, prefix string) ([]string, error) {
	result, err := kvd.list(ctx, prefix)
	if err != nil {
		return nil, err
	}
	return keysOf(ctx, result)
}

// keysOf reads every key result holds, and closes it. The store ends a query its
// context ended as it ends a finished one, so an ended context is the scan's error.
func keysOf(ctx context.Context, result query.Results) ([]string, error) {
	defer result.Close()
	keys := make([]string, 0)
	for entry := range result.Next() {
		if entry.Error != nil {
			return nil, entry.Error
		}
		keys = append(keys, entry.Key)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}

func (kvd *kvDatabase) list(ctx context.Context, prefix string) (query.Results, error) {
	return kvd.datastore.Query(ctx, query.Query{
		Prefix:   prefix,
		KeysOnly: true,
	})
}

func (kvd *kvDatabase) Batch(ctx context.Context) (kvdb.Batch, error) {
	b, err := kvd.datastore.Batch(ctx)
	if err != nil {
		return nil, err
	}
	return &batch{ctx: ctx, store: b}, nil
}

type batch struct {
	ctx   context.Context
	store ds.Batch
}

func (b *batch) Put(key string, value []byte) error {
	k := ds.NewKey(key)
	return b.store.Put(b.ctx, k, value)
}

func (b *batch) Delete(key string) error {
	k := ds.NewKey(key)
	return b.store.Delete(b.ctx, k)
}

func (b *batch) Commit() error {
	return b.store.Commit(b.ctx)
}

func (kvd *kvDatabase) Sync(ctx context.Context, key string) error {
	k := ds.NewKey(key)
	return kvd.datastore.Sync(ctx, k)
}
