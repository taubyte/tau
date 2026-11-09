// Package pebbleds is a go-datastore over pebble, with a native API that lends
// the store's own bytes, and a block store over it. Keys are their ds.Key
// string's bytes, so a store any such go-datastore over pebble wrote opens
// unchanged. A write is synced within syncWithin, or at once by Sync. A slice the native
// API lends (View, Scan, Range, Batch.Set, Batch.Remove) is valid only until
// the callback returns.
package pebbleds

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/bloom"
	ds "github.com/ipfs/go-datastore"
	logging "github.com/ipfs/go-log/v2"
)

var ErrClosed = errors.New("pebbleds: closed")

var logger = logging.Logger("pebble")

// syncWithin bounds how long a write stays volatile. Pebble keeps a write that
// asks for no sync in its own buffer, not the system's, until a log block fills or
// a sync asks for it, so a process that dies takes it however long ago it was made.
// The first write no sync covers arms one this long after it.
const syncWithin = 100 * time.Millisecond

type Store struct {
	db    *pebble.DB
	write *pebble.WriteOptions
	noWAL bool
	// volatile is a store whose writes wait for a sync: they ask for none, and go
	// to a log.
	volatile bool
	// dirty is a write no sync has covered yet. The write that sets it arms flush,
	// one timer for the store's life, so arming it allocates nothing.
	dirty atomic.Bool
	flush *time.Timer
	// cache is the block cache Open made, released after the database
	// closes; nil when the caller gave one.
	cache *pebble.Cache

	written atomic.Int64
	// users counts the calls in flight; Close waits for them.
	users  atomic.Int64
	closed atomic.Bool
	mu     sync.Mutex
	idle   *sync.Cond
	// The open queries and readers, which Close ends.
	queries map[*queryIter]struct{}
	readers map[*Reader]struct{}
	blocks  blockMemory
}

var (
	_ ds.Batching            = (*Store)(nil)
	_ ds.PersistentDatastore = (*Store)(nil)
)

type Option func(*config)

type config struct {
	pebble *pebble.Options
	write  *pebble.WriteOptions
}

// Fields left zero take pebble's defaults.
func WithPebbleOptions(o *pebble.Options) Option {
	return func(c *config) { c.pebble = o }
}

// WithWriteOptions writes with o instead of pebble.NoSync.
func WithWriteOptions(o *pebble.WriteOptions) Option {
	return func(c *config) { c.write = o }
}

const defaultCacheSize = 64 << 20

func Open(path string, opts ...Option) (*Store, error) {
	c := config{write: pebble.NoSync}
	for _, o := range opts {
		o(&c)
	}
	po := c.pebble
	if po == nil {
		po = &pebble.Options{}
	}
	if po.Logger == nil {
		po.Logger = logger
	}
	// A bloom filter per table answers a Get of an absent key, as a block
	// store's Has mostly is, without reading the table. Every level takes
	// the level above's.
	if po.Levels[0].FilterPolicy == nil {
		po.Levels[0].FilterPolicy = bloom.FilterPolicy(10)
	}
	// pebble releases a cache it makes from CacheSize as Open returns, and
	// the database is then left with one that keeps nothing: the store makes
	// its own and keeps it.
	var cache *pebble.Cache
	if po.Cache == nil {
		size := po.CacheSize
		if size == 0 {
			size = defaultCacheSize
		}
		cache = pebble.NewCache(size)
		po.Cache = cache
	}
	po.EnsureDefaults()
	db, err := pebble.Open(path, po)
	if err != nil {
		if cache != nil {
			cache.Unref()
		}
		return nil, fmt.Errorf("pebbleds: open %s: %w", path, err)
	}
	s := &Store{db: db, write: c.write, noWAL: po.DisableWAL, volatile: !po.DisableWAL && !c.write.GetSync(), cache: cache, queries: map[*queryIter]struct{}{}, readers: map[*Reader]struct{}{}}
	s.idle = sync.NewCond(&s.mu)
	if s.volatile {
		s.flush = time.AfterFunc(syncWithin, s.flushed)
		s.flush.Stop()
	}
	return s, nil
}

// Every admitted call leaves.
func (s *Store) enter() bool {
	s.users.Add(1)
	if s.closed.Load() {
		s.leave()
		return false
	}
	return true
}

func (s *Store) leave() {
	if s.users.Add(-1) == 0 && s.closed.Load() {
		s.mu.Lock()
		s.idle.Broadcast()
		s.mu.Unlock()
	}
}

// pebble only reads or copies a key, so nothing writes through the lent slice.
func keyBytes(k ds.Key) []byte {
	s := k.String()
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

// View returns ds.ErrNotFound for a key not stored.
func (s *Store) View(key []byte, fn func(v []byte) error) error {
	if !s.enter() {
		return ErrClosed
	}
	defer s.leave()
	v, closer, err := s.db.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return ds.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("pebbleds: get: %w", err)
	}
	err = fn(v)
	if cerr := closer.Close(); err == nil {
		err = cerr
	}
	return err
}

// Get copies the value.
func (s *Store) Get(ctx context.Context, key ds.Key) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []byte
	err := s.View(keyBytes(key), func(v []byte) error {
		out = append(make([]byte, 0, len(v)), v...)
		return nil
	})
	return out, err
}

// Has costs a Get.
func (s *Store) Has(ctx context.Context, key ds.Key) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	err := s.View(keyBytes(key), func([]byte) error { return nil })
	if errors.Is(err, ds.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) GetSize(ctx context.Context, key ds.Key) (int, error) {
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	size := -1
	err := s.View(keyBytes(key), func(v []byte) error {
		size = len(v)
		return nil
	})
	return size, err
}

func (s *Store) Put(ctx context.Context, key ds.Key, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.enter() {
		return ErrClosed
	}
	defer s.leave()
	if err := s.db.Set(keyBytes(key), value, s.write); err != nil {
		return fmt.Errorf("pebbleds: set: %w", err)
	}
	s.noteBlockSet(keyBytes(key))
	s.written.Add(int64(len(key.String()) + len(value)))
	s.wrote()
	return nil
}

// Every Put, and every Set of a committed batch.
func (s *Store) Written() int64 { return s.written.Load() }

// Removing an absent key is not an error.
func (s *Store) Delete(ctx context.Context, key ds.Key) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.enter() {
		return ErrClosed
	}
	defer s.leave()
	if err := s.db.Delete(keyBytes(key), s.write); err != nil {
		return fmt.Errorf("pebbleds: delete: %w", err)
	}
	if isBlockKey(keyBytes(key)) {
		s.deleted()
	}
	s.wrote()
	return nil
}

// wrote arms a sync for a write no sync covers yet. It is called once the write
// has landed, so a sync that clears dirty before it starts covers every write that
// found dirty set. A write is a call in flight, so none arms after Close stops it.
func (s *Store) wrote() {
	if s.volatile && s.dirty.CompareAndSwap(false, true) {
		s.flush.Reset(syncWithin)
	}
}

// flushed is the sync a write armed, unless one since covered it.
func (s *Store) flushed() {
	if !s.dirty.Load() {
		return
	}
	if err := s.Sync(context.Background(), ds.Key{}); err != nil && !errors.Is(err, ErrClosed) {
		logger.Errorf("sync: %v", err)
	}
}

// Sync makes every earlier write durable whatever its prefix: pebble has one
// log, synced only as part of a write, so it writes an empty synced record.
// Without a log there is nothing to sync.
func (s *Store) Sync(ctx context.Context, _ ds.Key) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.enter() {
		return ErrClosed
	}
	defer s.leave()
	if s.noWAL {
		return nil
	}
	s.dirty.Store(false)
	if err := s.db.LogData(nil, pebble.Sync); err != nil {
		return fmt.Errorf("pebbleds: sync: %w", err)
	}
	return nil
}

// KeepsPrefix reports that a crash leaves the store a prefix of its writes:
// pebble writes them to one log, in commit order, and replays an unbroken
// run of it, and it flushes its memtables in order.
func (s *Store) KeepsPrefix() bool { return true }

func (s *Store) DiskUsage(context.Context) (uint64, error) {
	if !s.enter() {
		return 0, ErrClosed
	}
	defer s.leave()
	return s.db.Metrics().DiskSpaceUsage(), nil
}

// Close ends open queries and readers, waits for calls in flight, flushes and
// closes. It must not be called from inside a callback the store is running.
func (s *Store) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	// A query opened by a call still in flight registers before that call
	// leaves, so once none are in flight every open query is listed.
	s.mu.Lock()
	for s.users.Load() != 0 {
		s.idle.Wait()
	}
	open := make([]*queryIter, 0, len(s.queries))
	for q := range s.queries {
		open = append(open, q)
	}
	readers := make([]*Reader, 0, len(s.readers))
	for r := range s.readers {
		readers = append(readers, r)
	}
	clear(s.readers)
	s.mu.Unlock()
	if s.flush != nil {
		s.flush.Stop()
	}
	for _, q := range open {
		q.end()
	}
	for _, r := range readers {
		r.it.Close()
	}
	if err := s.db.Flush(); err != nil {
		logger.Errorf("flush before close: %v", err)
	}
	err := s.db.Close()
	if s.cache != nil {
		s.cache.Unref()
	}
	return err
}
