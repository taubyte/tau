package raft

import (
	"context"
	"sync"
	"testing"

	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
	dsync "github.com/ipfs/go-datastore/sync"
)

// faultStore fails on the keys a test names: a map datastore always succeeds,
// so a swallowed store error and a healthy store read alike.
type faultStore struct {
	ds.Batching

	mu         sync.Mutex
	getFail    map[string]error
	putFail    map[string]error
	deleteFail map[string]error
	stageFail  map[string]error
	queryFail  map[string]error
	scanFail   map[string]error
	batchFail  error
}

func newFaultStore() *faultStore {
	return &faultStore{
		Batching:   dsync.MutexWrap(ds.NewMapDatastore()),
		getFail:    make(map[string]error),
		putFail:    make(map[string]error),
		deleteFail: make(map[string]error),
		stageFail:  make(map[string]error),
		queryFail:  make(map[string]error),
		scanFail:   make(map[string]error),
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

func (s *faultStore) failDelete(key string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteFail[key] = err
}

// failStaging makes a batch refuse to stage a write or delete of key, which
// is a batch failing before its commit is ever tried.
func (s *faultStore) failStaging(key string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stageFail[key] = err
}

func (s *faultStore) failQuery(prefix string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queryFail[prefix] = err
}

// failScan makes every later query of prefix return what it holds and then
// err, the shape of an iterator that hits a bad block partway through.
func (s *faultStore) failScan(prefix string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scanFail[prefix] = err
}

func (s *faultStore) failBatches(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batchFail = err
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

func (s *faultStore) Delete(ctx context.Context, key ds.Key) error {
	if err := s.faultFor(s.deleteFail, key); err != nil {
		return err
	}
	return s.Batching.Delete(ctx, key)
}

func (s *faultStore) Query(ctx context.Context, q query.Query) (query.Results, error) {
	prefix := ds.NewKey(q.Prefix)
	if err := s.faultFor(s.queryFail, prefix); err != nil {
		return nil, err
	}
	results, err := s.Batching.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	scanErr := s.faultFor(s.scanFail, prefix)
	if scanErr == nil {
		return results, nil
	}
	entries, err := results.Rest()
	if err != nil {
		return nil, err
	}
	return query.ResultsFromIterator(q, query.Iterator{
		Next: func() (query.Result, bool) {
			if len(entries) > 0 {
				next := entries[0]
				entries = entries[1:]
				return query.Result{Entry: next}, true
			}
			if scanErr != nil {
				err := scanErr
				scanErr = nil
				return query.Result{Error: err}, true
			}
			return query.Result{}, false
		},
	}), nil
}

func (s *faultStore) Batch(ctx context.Context) (ds.Batch, error) {
	s.mu.Lock()
	err := s.batchFail
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &faultBatch{store: s}, nil
}

// faultBatch is all-or-nothing, as pebble's Batch.Commit is one WAL record.
// go-datastore's basic batch, which a map datastore hands out, replays its
// operations one at a time and stops on the first error.
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
	if err := b.store.faultFor(b.store.stageFail, key); err != nil {
		return err
	}
	b.staged = append(b.staged, stagedWrite{key: key, value: value})
	return nil
}

func (b *faultBatch) Delete(ctx context.Context, key ds.Key) error {
	if err := b.store.faultFor(b.store.stageFail, key); err != nil {
		return err
	}
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
