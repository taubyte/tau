package raft

import (
	"context"
	"sync"
	"testing"

	ds "github.com/ipfs/go-datastore"
	dsync "github.com/ipfs/go-datastore/sync"
)

// faultStore is a datastore that fails on the keys a test names, because
// NOTHING ELSE IN THIS PACKAGE'S TESTS CAN MAKE A STORE FAIL. Every other test
// here runs on a map datastore, which always succeeds, so a swallowed store
// error and a store that is fine read exactly alike — which is how two of the
// defects these tests pin stayed invisible.
type faultStore struct {
	ds.Batching

	mu      sync.Mutex
	getFail map[string]error
	putFail map[string]error
}

func newFaultStore() *faultStore {
	return &faultStore{
		Batching: dsync.MutexWrap(ds.NewMapDatastore()),
		getFail:  make(map[string]error),
		putFail:  make(map[string]error),
	}
}

// failGet makes every later read of key answer err rather than a value or
// ds.ErrNotFound. That is the shape go-ds-pebble reports a disk error or a
// closed database in: wrapped, and never ds.ErrNotFound.
func (s *faultStore) failGet(key string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getFail[key] = err
}

// failPut makes every later write of key fail, whether it arrives on its own
// or inside a batch.
func (s *faultStore) failPut(key string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putFail[key] = err
}

func (s *faultStore) faultFor(faults map[string]error, key ds.Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return faults[key.String()]
}

func (s *faultStore) Get(ctx context.Context, key ds.Key) ([]byte, error) {
	if err := s.faultFor(s.getFail, key); err != nil {
		return nil, err
	}
	return s.Batching.Get(ctx, key)
}

func (s *faultStore) Put(ctx context.Context, key ds.Key, value []byte) error {
	if err := s.faultFor(s.putFail, key); err != nil {
		return err
	}
	return s.Batching.Put(ctx, key, value)
}

func (s *faultStore) Batch(ctx context.Context) (ds.Batch, error) {
	return &faultBatch{store: s}, nil
}

// faultBatch is ALL-OR-NOTHING ON PURPOSE, and that is the whole reason this
// fake exists rather than a map datastore with a hook on it. Production runs
// on pebble, whose Batch.Commit is one WAL record applied under one sequence
// number; go-datastore's basic batch, which is what a map datastore hands out,
// replays the staged operations one at a time and stops on the first error. A
// fake copying the basic batch would measure the fix's shape and not its
// effect, so this one models the store a node actually has.
type faultBatch struct {
	store  *faultStore
	staged []stagedWrite
}

type stagedWrite struct {
	key    ds.Key
	value  []byte
	delete bool
}

func (b *faultBatch) Put(ctx context.Context, key ds.Key, value []byte) error {
	b.staged = append(b.staged, stagedWrite{key: key, value: value})
	return nil
}

func (b *faultBatch) Delete(ctx context.Context, key ds.Key) error {
	b.staged = append(b.staged, stagedWrite{key: key, delete: true})
	return nil
}

func (b *faultBatch) Commit(ctx context.Context) error {
	for _, write := range b.staged {
		if err := b.store.faultFor(b.store.putFail, write.key); err != nil {
			return err
		}
	}
	for _, write := range b.staged {
		if write.delete {
			if err := b.store.Batching.Delete(ctx, write.key); err != nil {
				return err
			}
			continue
		}
		if err := b.store.Batching.Put(ctx, write.key, write.value); err != nil {
			return err
		}
	}
	return nil
}

// absent reports whether the FSM holds nothing at key, reading around Get so
// that a store fault cannot be mistaken for the answer.
func absent(t *testing.T, store ds.Batching, prefix, key string) bool {
	t.Helper()

	has, err := store.Has(context.Background(), ds.NewKey(prefix+"/data/"+key))
	if err != nil {
		t.Fatalf("asking the store for %q: %v", key, err)
	}
	return !has
}
