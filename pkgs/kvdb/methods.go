package kvdb

import (
	"context"

	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	"github.com/taubyte/go-interfaces/kvdb"
)

// retrieves a value from the database
func (kvd *kvDatabase) Get(ctx context.Context, key string) ([]byte, error) {
	k := ds.NewKey(key)
	return kvd.datastore.Get(ctx, k)
}

// inserts or updates a value in the database
func (kvd *kvDatabase) Put(ctx context.Context, key string, v []byte) error {
	k := ds.NewKey(key)
	return kvd.datastore.Put(ctx, k, v)
}

func (kvd *kvDatabase) Delete(ctx context.Context, key string) error {
	k := ds.NewKey(key)
	return kvd.datastore.Delete(ctx, k)
}
// returns a list of keys in the database that have the given prefix
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
	// iterate over the query results and send the keys to the string channel.
	c := make(chan string, QueryBufferSize)
	go func() {
		defer close(c)
		defer result.Close()
		source := result.Next()
		for {
			select {
			case entry, ok := <-source:
				if !ok || entry.Error != nil {
					return
				}

				c <- entry.Key
			case <-ctx.Done():
				return
			}
		}
	}()

	return c, nil
}

// list queries the underlying data store with the provided context and prefix to retrieve results matching the prefix.
func (kvd *kvDatabase) list(ctx context.Context, prefix string) (query.Results, error) {
	return kvd.datastore.Query(ctx, query.Query{
		Prefix:   prefix,
		KeysOnly: true,
	})
}

// Batch creates a new Batch object that allows for batch operations on the key-value database.
func (kvd *kvDatabase) Batch(ctx context.Context) (kvdb.Batch, error) {
	b, err := kvd.datastore.Batch(ctx)
	if err != nil {
		return nil, err
	}
	return &Batch{ctx: ctx, store: b}, nil
}

type Batch struct {
	ctx   context.Context
	store ds.Batch
}

func (b *Batch) Put(key string, value []byte) error {
	k := ds.NewKey(key)
	return b.store.Put(b.ctx, k, value)
}

func (b *Batch) Delete(key string) error {
	k := ds.NewKey(key)
	return b.store.Delete(b.ctx, k)
}

func (b *Batch) Commit() error {
	return b.store.Commit(b.ctx)
}

func (kvd *kvDatabase) Sync(ctx context.Context, key string) error {
	k := ds.NewKey(key)
	return kvd.datastore.Sync(ctx, k)
}
