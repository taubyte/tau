package kvdb

import (
	"context"
	"errors"

	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	"github.com/taubyte/tau/core/kvdb"
)

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

func (kvd *kvDatabase) List(ctx context.Context, prefix string) ([]string, error) {
	result, err := kvd.list(ctx, prefix)
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0)
	for entry := range result.Next() {
		keys = append(keys, entry.Key)
	}
	return keys, nil
}

func (kvd *kvDatabase) ListAsync(ctx context.Context, prefix string) (chan string, error) {
	result, err := kvd.list(ctx, prefix)
	if err != nil {
		return nil, err
	}

	c := make(chan string, QueryBufferSize)
	go func() {
		defer close(c)
		defer result.Close()
		source := result.Next()
		for {
			select {
			case <-ctx.Done():
				return
			case entry, ok := <-source:
				if !ok || entry.Error != nil {
					return
				}

				c <- entry.Key
			}
		}
	}()

	return c, nil
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
