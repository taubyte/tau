package kvdb

// A small fault-injection harness (Item H) used to reach the many small
// "if err != nil { ... }" branches scattered across crdt.go, heads.go,
// set.go, migrations.go and merklecrdt.go that wrap the underlying
// ds.Datastore: most of them are only reachable when that underlying store
// itself starts failing (a real-world condition -- disk errors, a closed
// DB, etc -- that the existing happy-path-only harness never exercises).

import (
	"context"
	"errors"
	"sync"

	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	dssync "github.com/ipfs/go-datastore/sync"
)

// faultyDatastore wraps a real ds.Datastore (which must also implement
// ds.Batching, as ds.NewMapDatastore() does) and can be configured to fail
// specific operations on demand via SetFail. By default (fail == nil) it
// behaves exactly like the wrapped store.
type faultyDatastore struct {
	ds.Datastore
	mu   sync.Mutex
	fail func(op string, key ds.Key) error
}

// newFaultyDatastore wraps inner with a mutex (as every other store built by
// this suite's harness does, e.g. makeStore's dssync.MutexWrap): a
// *Datastore built on top of it runs several background goroutines (repair,
// rebroadcast, dagWorkers, ...) that access the underlying store
// concurrently with the test's own goroutine, and plain
// ds.NewMapDatastore() is not safe for that on its own.
func newFaultyDatastore(inner ds.Datastore) *faultyDatastore {
	return &faultyDatastore{Datastore: dssync.MutexWrap(inner)}
}

// SetFail installs (or, with nil, clears) the failure predicate. Safe to
// call at any point, including after the store has already been used
// successfully (e.g. to fail only a later operation).
func (f *faultyDatastore) SetFail(fn func(op string, key ds.Key) error) {
	f.mu.Lock()
	f.fail = fn
	f.mu.Unlock()
}

func (f *faultyDatastore) check(op string, key ds.Key) error {
	f.mu.Lock()
	fn := f.fail
	f.mu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(op, key)
}

func (f *faultyDatastore) Get(ctx context.Context, key ds.Key) ([]byte, error) {
	if err := f.check("Get", key); err != nil {
		return nil, err
	}
	return f.Datastore.Get(ctx, key)
}

func (f *faultyDatastore) Put(ctx context.Context, key ds.Key, value []byte) error {
	if err := f.check("Put", key); err != nil {
		return err
	}
	return f.Datastore.Put(ctx, key, value)
}

func (f *faultyDatastore) Delete(ctx context.Context, key ds.Key) error {
	if err := f.check("Delete", key); err != nil {
		return err
	}
	return f.Datastore.Delete(ctx, key)
}

func (f *faultyDatastore) Has(ctx context.Context, key ds.Key) (bool, error) {
	if err := f.check("Has", key); err != nil {
		return false, err
	}
	return f.Datastore.Has(ctx, key)
}

func (f *faultyDatastore) GetSize(ctx context.Context, key ds.Key) (int, error) {
	if err := f.check("GetSize", key); err != nil {
		return 0, err
	}
	return f.Datastore.GetSize(ctx, key)
}

func (f *faultyDatastore) Sync(ctx context.Context, prefix ds.Key) error {
	if err := f.check("Sync", prefix); err != nil {
		return err
	}
	return f.Datastore.Sync(ctx, prefix)
}

func (f *faultyDatastore) Query(ctx context.Context, q query.Query) (query.Results, error) {
	if err := f.check("Query", ds.NewKey(q.Prefix)); err != nil {
		return nil, err
	}
	return f.Datastore.Query(ctx, q)
}

func (f *faultyDatastore) Batch(ctx context.Context) (ds.Batch, error) {
	if err := f.check("Batch", ds.Key{}); err != nil {
		return nil, err
	}
	b, err := f.Datastore.(ds.Batching).Batch(ctx)
	if err != nil {
		return nil, err
	}
	return &faultyBatch{Batch: b, parent: f}, nil
}

// faultyBatch wraps a real ds.Batch so that Put/Delete/Commit issued through
// a crdtBatch can be failed the same way as their non-batched counterparts.
type faultyBatch struct {
	ds.Batch
	parent *faultyDatastore
}

func (b *faultyBatch) Put(ctx context.Context, key ds.Key, value []byte) error {
	if err := b.parent.check("BatchPut", key); err != nil {
		return err
	}
	return b.Batch.Put(ctx, key, value)
}

func (b *faultyBatch) Delete(ctx context.Context, key ds.Key) error {
	if err := b.parent.check("BatchDelete", key); err != nil {
		return err
	}
	return b.Batch.Delete(ctx, key)
}

func (b *faultyBatch) Commit(ctx context.Context) error {
	if err := b.parent.check("Commit", ds.Key{}); err != nil {
		return err
	}
	return b.Batch.Commit(ctx)
}

// errFault is the sentinel error every injected failure wraps, so tests can
// assert on it with errors.Is.
var errFault = errors.New("injected fault")

// failAlways returns a fail predicate that fails every call to any of the
// given ops (or every op, if none given), regardless of key.
func failAlways(ops ...string) func(op string, key ds.Key) error {
	opSet := make(map[string]bool, len(ops))
	for _, o := range ops {
		opSet[o] = true
	}
	return func(op string, key ds.Key) error {
		if len(opSet) > 0 && !opSet[op] {
			return nil
		}
		return errFault
	}
}
