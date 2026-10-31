// Package pebbleds is a go-datastore over pebble, with a native API that
// lends the store's own bytes instead of copying them, and a block store
// over it.
//
// Keys are stored as the bytes of their ds.Key string, so a store written by
// any go-datastore over pebble that does the same opens unchanged. Writes are
// not synced unless Sync is called; Sync makes every earlier write durable.
//
// The native API (View, Scan, Range, Batch.Set and Batch.Remove) takes and
// lends []byte. A lent slice is valid only until the callback returns: copy
// what must outlive it.
package pebbleds

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/bloom"
	ds "github.com/ipfs/go-datastore"
	logging "github.com/ipfs/go-log/v2"
)

// ErrClosed is returned by every call made after Close.
var ErrClosed = errors.New("pebbleds: closed")

var logger = logging.Logger("pebble")

// Store is a pebble database behind the go-datastore interfaces.
type Store struct {
	db    *pebble.DB
	write *pebble.WriteOptions
	noWAL bool
	// cache is the block cache Open made, released after the database
	// closes; nil when the caller gave one.
	cache *pebble.Cache

	// written counts the bytes of the keys and values written.
	written atomic.Int64
	// users counts the calls in flight; Close waits for them.
	users  atomic.Int64
	closed atomic.Bool
	mu     sync.Mutex
	idle   *sync.Cond
	// queries are the open query results, which Close ends.
	queries map[*queryIter]struct{}
	// blocks is what the store knows in memory of the blocks it holds.
	blocks blockMemory
}

var (
	_ ds.Batching            = (*Store)(nil)
	_ ds.PersistentDatastore = (*Store)(nil)
)

// Option configures Open.
type Option func(*config)

type config struct {
	pebble *pebble.Options
	write  *pebble.WriteOptions
}

// WithPebbleOptions opens the database with o. Fields left zero take
// pebble's defaults.
func WithPebbleOptions(o *pebble.Options) Option {
	return func(c *config) { c.pebble = o }
}

// WithWriteOptions writes with o instead of pebble.NoSync.
func WithWriteOptions(o *pebble.WriteOptions) Option {
	return func(c *config) { c.write = o }
}

// defaultCacheSize is the block cache of a store not given one or its size.
const defaultCacheSize = 64 << 20

// Open opens, or creates, the store at path.
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
	s := &Store{db: db, write: c.write, noWAL: po.DisableWAL, cache: cache, queries: map[*queryIter]struct{}{}}
	s.idle = sync.NewCond(&s.mu)
	return s, nil
}

// enter admits a call, unless the store is closed. Every admitted call
// leaves.
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

// keyBytes lends k's bytes. pebble only reads a key it is given, or copies
// it, so nothing writes through the slice.
func keyBytes(k ds.Key) []byte {
	s := k.String()
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

// View calls fn with the value stored at key, or returns ds.ErrNotFound.
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

// Get returns a copy of the value stored at key.
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

// Has reports whether key is stored. It costs a Get.
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

// GetSize returns the length of the value stored at key.
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

// Put stores value at key.
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
	return nil
}

// Written is how many bytes of keys and values the store has written:
// every Put, and every Set of a committed batch.
func (s *Store) Written() int64 { return s.written.Load() }

// Delete removes key. Removing an absent key is not an error.
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
	return nil
}

// Sync makes every write made before it durable, whatever its prefix:
// pebble has one log. It writes an empty synced log record, since pebble
// syncs its log only as part of a write. Without a log there is nothing to
// sync.
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
	if err := s.db.LogData(nil, pebble.Sync); err != nil {
		return fmt.Errorf("pebbleds: sync: %w", err)
	}
	return nil
}

// DiskUsage is the space pebble's files take.
func (s *Store) DiskUsage(context.Context) (uint64, error) {
	if !s.enter() {
		return 0, ErrClosed
	}
	defer s.leave()
	return s.db.Metrics().DiskSpaceUsage(), nil
}

// Close ends open queries, waits for calls in flight, flushes and closes
// the database. Calls made after it return ErrClosed. It must not be called
// from inside a callback the store is running.
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
	s.mu.Unlock()
	for _, q := range open {
		q.end()
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
