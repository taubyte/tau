package helpers

import (
	"context"
	"errors"
	"sync"

	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
)

// ErrDatastoreClosed is returned by operations on a closed datastore.
var ErrDatastoreClosed = errors.New("datastore closed")

// closeGuard makes using a closed datastore an error. Pebble panics instead, and
// goroutines of a service being stopped can still be starting queries: the panic
// took down the whole process, with every other service in it. Iterators already
// open are safe, the pebble datastore ends them itself on close.
type closeGuard struct {
	datastore.Batching
	lock   sync.RWMutex
	closed bool
}

func guardClose(store datastore.Batching) datastore.Batching {
	return &closeGuard{Batching: store}
}

// enter holds off Close until the returned func is called.
func (g *closeGuard) enter() (func(), error) {
	g.lock.RLock()
	if g.closed {
		g.lock.RUnlock()
		return nil, ErrDatastoreClosed
	}
	return g.lock.RUnlock, nil
}

func (g *closeGuard) Get(ctx context.Context, key datastore.Key) ([]byte, error) {
	leave, err := g.enter()
	if err != nil {
		return nil, err
	}
	defer leave()
	return g.Batching.Get(ctx, key)
}

func (g *closeGuard) Has(ctx context.Context, key datastore.Key) (bool, error) {
	leave, err := g.enter()
	if err != nil {
		return false, err
	}
	defer leave()
	return g.Batching.Has(ctx, key)
}

func (g *closeGuard) GetSize(ctx context.Context, key datastore.Key) (int, error) {
	leave, err := g.enter()
	if err != nil {
		return -1, err
	}
	defer leave()
	return g.Batching.GetSize(ctx, key)
}

func (g *closeGuard) Query(ctx context.Context, q query.Query) (query.Results, error) {
	leave, err := g.enter()
	if err != nil {
		return nil, err
	}
	defer leave()
	return g.Batching.Query(ctx, q)
}

func (g *closeGuard) Put(ctx context.Context, key datastore.Key, value []byte) error {
	leave, err := g.enter()
	if err != nil {
		return err
	}
	defer leave()
	return g.Batching.Put(ctx, key, value)
}

func (g *closeGuard) Delete(ctx context.Context, key datastore.Key) error {
	leave, err := g.enter()
	if err != nil {
		return err
	}
	defer leave()
	return g.Batching.Delete(ctx, key)
}

func (g *closeGuard) Sync(ctx context.Context, prefix datastore.Key) error {
	leave, err := g.enter()
	if err != nil {
		return err
	}
	defer leave()
	return g.Batching.Sync(ctx, prefix)
}

func (g *closeGuard) Batch(ctx context.Context) (datastore.Batch, error) {
	leave, err := g.enter()
	if err != nil {
		return nil, err
	}
	defer leave()
	batch, err := g.Batching.Batch(ctx)
	if err != nil {
		return nil, err
	}
	return &guardedBatch{Batch: batch, guard: g}, nil
}

func (g *closeGuard) Close() error {
	g.lock.Lock()
	defer g.lock.Unlock()
	if g.closed {
		return nil
	}
	g.closed = true
	return g.Batching.Close()
}

type guardedBatch struct {
	datastore.Batch
	guard *closeGuard
}

func (b *guardedBatch) Commit(ctx context.Context) error {
	leave, err := b.guard.enter()
	if err != nil {
		return err
	}
	defer leave()
	return b.Batch.Commit(ctx)
}
