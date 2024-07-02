package mem

import (
	"context"
	"errors"
	"sort"
	"sync"

	datastore "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
)

var _ datastore.Datastore = (*Datastore)(nil)
var _ datastore.Batching = (*Datastore)(nil)

var ErrClosed = errors.New("datastore closed")

func New() *Datastore {
	return &Datastore{
		store: make(map[datastore.Key][]byte),
	}
}

type Datastore struct {
	mu    sync.RWMutex
	store map[datastore.Key][]byte
}

func (ds *Datastore) Put(ctx context.Context, key datastore.Key, value []byte) error {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	if ds.store == nil {
		return ErrClosed
	}

	ds.store[key] = value
	return nil
}

func (ds *Datastore) Sync(ctx context.Context, prefix datastore.Key) error {
	if ds.store == nil {
		return ErrClosed
	}

	return nil
}

func (ds *Datastore) Get(ctx context.Context, key datastore.Key) (value []byte, err error) {
	ds.mu.RLock()
	defer ds.mu.RUnlock()

	if ds.store == nil {
		return nil, ErrClosed
	}

	if d, ok := ds.store[key]; ok {
		return d, nil
	}
	return nil, datastore.ErrNotFound
}

func (ds *Datastore) GetSize(ctx context.Context, key datastore.Key) (size int, err error) {
	ds.mu.RLock()
	defer ds.mu.RUnlock()

	if ds.store == nil {
		return -1, ErrClosed
	}

	if d, ok := ds.store[key]; ok {
		return len(d), nil
	}
	return -1, datastore.ErrNotFound
}

func (ds *Datastore) Has(ctx context.Context, key datastore.Key) (exists bool, err error) {
	ds.mu.RLock()
	defer ds.mu.RUnlock()

	if ds.store == nil {
		return false, ErrClosed
	}

	_, ok := ds.store[key]
	return ok, nil
}

func (ds *Datastore) Delete(ctx context.Context, key datastore.Key) (err error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	if ds.store == nil {
		return ErrClosed
	}

	delete(ds.store, key)
	return nil
}

func (ds *Datastore) Query(ctx context.Context, q query.Query) (query.Results, error) {
	ds.mu.RLock()
	defer ds.mu.RUnlock()

	if ds.store == nil {
		return nil, ErrClosed
	}

	var entries []query.Entry
	for k, v := range ds.store {
		e := query.Entry{Key: k.String(), Size: len(v)}
		if !q.KeysOnly {
			e.Value = make([]byte, len(v))
			copy(e.Value, v)
		}
		entries = append(entries, e)
	}

	if len(q.Filters) > 0 {
		var filteredEntries []query.Entry
		for _, entry := range entries {
			include := true
			for _, filter := range q.Filters {
				if !filter.Filter(entry) {
					include = false
					break
				}
			}
			if include {
				filteredEntries = append(filteredEntries, entry)
			}
		}
		entries = filteredEntries
	}

	if len(q.Orders) > 0 {
		for _, order := range q.Orders {
			sort.Slice(entries, func(i, j int) bool {
				return order.Compare(entries[i], entries[j]) < 0
			})
		}
	}

	if q.Offset > 0 {
		if q.Offset < len(entries) {
			entries = entries[q.Offset:]
		} else {
			entries = nil
		}
	}
	if q.Limit > 0 && q.Limit < len(entries) {
		entries = entries[:q.Limit]
	}

	return query.ResultsWithEntries(q, entries), nil
}

func (ds *Datastore) Close() error {
	ds.store = nil
	return nil
}

type Batch struct {
	ds   *Datastore
	ops  []operation
	lock sync.Mutex
}

type operation struct {
	delete bool
	key    datastore.Key
	value  []byte
}

func (ds *Datastore) Batch(ctx context.Context) (datastore.Batch, error) {
	if ds.store == nil {
		return nil, ErrClosed
	}

	return &Batch{
		ds:  ds,
		ops: make([]operation, 0),
	}, nil
}

func (b *Batch) Put(ctx context.Context, key datastore.Key, value []byte) error {
	b.lock.Lock()
	defer b.lock.Unlock()

	if b.ds.store == nil {
		return ErrClosed
	}

	b.ops = append(b.ops, operation{delete: false, key: key, value: value})
	return nil
}

func (b *Batch) Delete(ctx context.Context, key datastore.Key) error {
	b.lock.Lock()
	defer b.lock.Unlock()

	if b.ds.store == nil {
		return ErrClosed
	}

	b.ops = append(b.ops, operation{delete: true, key: key})
	return nil
}

func (b *Batch) Commit(ctx context.Context) error {
	b.lock.Lock()
	defer b.lock.Unlock()

	b.ds.mu.Lock()
	defer b.ds.mu.Unlock()

	if b.ds.store == nil {
		return ErrClosed
	}

	for _, op := range b.ops {
		if op.delete {
			delete(b.ds.store, op.key)
		} else {
			b.ds.store[op.key] = op.value
		}
	}

	b.ops = nil
	return nil
}
