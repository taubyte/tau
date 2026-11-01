// Package cache is the key-value cache a workload reaches, with the two properties
// the vocabulary gives every cache: an entry lifetime and a ceiling on the whole.
//
// It is not a system of record, so a put that does not fit evicts rather than
// refuses: expired entries first, then the oldest-written. It is not LRU: tracking
// reads would be a write on every read, which two nodes make differently.
//
// Accounting is an in-memory index per node, seeded by a scan bounded by `size`.
// Under a replicated cache each peer enforces the same ceiling on its own copy; a
// shared counter row would be a key two nodes write differently.
package cache

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/taubyte/tau/pkg/storekey"
)

// Store is where entries live: the same four-method door services/secrets keeps
// over a kvdb.
type Store interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Put(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)
}

var (
	ErrNoStore  = errors.New("cache: a cache with no store holds nothing")
	ErrNoKey    = errors.New("cache: no key")
	ErrNotFound = errors.New("cache: no such entry")

	// ErrTooLarge is one value bigger than the whole cache. Evicting
	// everything would still not make it fit, so it is refused rather than
	// emptying the cache to fail anyway.
	ErrTooLarge = errors.New("cache: that value is larger than the whole cache")
)

// headerSize is the fixed prefix on every stored value: when it was written and
// when it expires, both unix nanoseconds, big-endian. It is in the value, not a
// second row that a replicated store could show half-applied.
const headerSize = 16

// Cache is one project-environment-resource's entries.
type Cache struct {
	store  Store
	prefix string

	// ttl is the declared entry lifetime. Zero means entries do not expire.
	ttl time.Duration
	// size is the ceiling in bytes, counting keys and values. Zero means none,
	// which the vocabulary does not allow but a caller composing one by hand
	// may want.
	size int64

	now func() time.Time

	mu    sync.Mutex
	index map[string]entry
	used  int64
}

type entry struct {
	size    int64
	written time.Time
	expires time.Time
}

// Config is what the compiled resource said about this cache.
type Config struct {
	// Prefix is where this cache's entries live in the store. The caller
	// composes it; this package appends a "/" and the key.
	Prefix string
	// TTL is `keys/ttl`. Zero means entries do not expire.
	TTL time.Duration
	// Size is `keys/size` in bytes. Zero means no ceiling.
	Size int64
}

// Option configures a Cache.
type Option func(*Cache)

// WithClock replaces time.Now, so a test can age an entry without sleeping.
func WithClock(now func() time.Time) Option {
	return func(c *Cache) {
		if now != nil {
			c.now = now
		}
	}
}

// Open builds a cache over a store and seeds its accounting. Starting empty would
// let a restarted node hold two ceilings' worth.
func Open(ctx context.Context, store Store, cfg Config, opts ...Option) (*Cache, error) {
	if store == nil {
		return nil, ErrNoStore
	}

	c := &Cache{
		store:  store,
		prefix: strings.TrimSuffix(cfg.Prefix, "/") + "/",
		ttl:    cfg.TTL,
		size:   cfg.Size,
		now:    time.Now,
		index:  map[string]entry{},
	}
	for _, apply := range opts {
		apply(c)
	}

	if err := c.seed(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// seed reads what this node already holds.
func (c *Cache) seed(ctx context.Context) error {
	keys, err := c.store.List(ctx, c.prefix)
	if err != nil {
		return fmt.Errorf("cache: reading what %s holds: %w", c.prefix, err)
	}

	for _, full := range keys {
		_, key, ok := strings.Cut(full, c.prefix)
		if !ok || key == "" {
			continue
		}

		raw, found, err := c.store.Get(ctx, full)
		if err != nil {
			return fmt.Errorf("cache: reading %s: %w", c.prefix, err)
		}
		if !found || len(raw) < headerSize {
			// A row that is not one of ours, or one a concurrent delete took
			// between the list and the read. Neither is countable and neither
			// is an error.
			continue
		}

		written, expires, value := decode(raw)
		c.index[key] = entry{size: sizeOf(key, value), written: written, expires: expires}
		c.used += c.index[key].size
	}
	return nil
}

// Get returns a value, or ErrNotFound. An expired entry is a miss and is deleted
// on the way out, since nothing else walks the cache.
func (c *Cache) Get(ctx context.Context, key string) ([]byte, error) {
	if key == "" {
		return nil, ErrNoKey
	}

	at, err := c.key(key)
	if err != nil {
		return nil, err
	}

	raw, found, err := c.store.Get(ctx, at)
	if err != nil {
		return nil, err
	}
	if !found || len(raw) < headerSize {
		c.forget(key)
		return nil, fmt.Errorf("%w: %s", ErrNotFound, key)
	}

	_, expires, value := decode(raw)
	if c.expired(expires) {
		if err := c.drop(ctx, key); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return value, nil
}

// Put writes a value, evicting to make room if it has to.
func (c *Cache) Put(ctx context.Context, key string, value []byte) error {
	if key == "" {
		return ErrNoKey
	}

	need := sizeOf(key, value)
	if c.size > 0 && need > c.size {
		return fmt.Errorf("%w: %s needs %d of %d bytes", ErrTooLarge, key, need, c.size)
	}

	if err := c.makeRoom(ctx, key, need); err != nil {
		return err
	}

	written := c.now()
	var expires time.Time
	if c.ttl > 0 {
		expires = written.Add(c.ttl)
	}

	at, err := c.key(key)
	if err != nil {
		return err
	}

	if err := c.store.Put(ctx, at, encode(written, expires, value)); err != nil {
		return err
	}

	c.mu.Lock()
	if old, ok := c.index[key]; ok {
		c.used -= old.size
	}
	c.index[key] = entry{size: need, written: written, expires: expires}
	c.used += need
	c.mu.Unlock()
	return nil
}

// Delete removes an entry. Deleting what is not there succeeds.
func (c *Cache) Delete(ctx context.Context, key string) error {
	if key == "" {
		return ErrNoKey
	}
	return c.drop(ctx, key)
}

// List names the live entries under a prefix, sorted. An expired entry is not
// listed, and is not deleted either: a listing is a read and a read that wrote
// would make two peers listing the same cache disagree about what they did.
func (c *Cache) List(ctx context.Context, prefix string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]string, 0, len(c.index))
	for key, e := range c.index {
		if !strings.HasPrefix(key, prefix) || c.expired(e.expires) {
			continue
		}
		out = append(out, key)
	}
	sort.Strings(out)
	return out, nil
}

// makeRoom drops expired entries, then the oldest-written ones, until need
// fits beside what is already there.
func (c *Cache) makeRoom(ctx context.Context, key string, need int64) error {
	if c.size == 0 {
		return nil
	}

	for _, victim := range c.victims(key, need) {
		if err := c.drop(ctx, victim); err != nil {
			return err
		}
	}
	return nil
}

// victims is which entries have to go, expired first and then oldest-written.
// It takes the lock once and answers with names, so the deletes below it are
// not holding it.
func (c *Cache) victims(key string, need int64) []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	free := c.size - c.used
	if replacing, ok := c.index[key]; ok {
		// Overwriting gives back what the old entry held.
		free += replacing.size
	}
	if free >= need {
		return nil
	}

	type candidate struct {
		key     string
		size    int64
		written time.Time
		expired bool
	}

	all := make([]candidate, 0, len(c.index))
	for k, e := range c.index {
		if k == key {
			continue
		}
		all = append(all, candidate{k, e.size, e.written, c.expired(e.expires)})
	}

	// Expired first — they are free to lose — then oldest-written. The name is
	// the final tiebreak so two nodes with the same entries evict the same one.
	sort.Slice(all, func(i, j int) bool {
		if all[i].expired != all[j].expired {
			return all[i].expired
		}
		if !all[i].written.Equal(all[j].written) {
			return all[i].written.Before(all[j].written)
		}
		return all[i].key < all[j].key
	})

	var out []string
	for _, c := range all {
		if free >= need {
			break
		}
		out = append(out, c.key)
		free += c.size
	}
	return out
}

// drop deletes an entry and forgets it. The store is written first: forgetting
// an entry the store still holds would leak its bytes out of the accounting
// until the next open.
func (c *Cache) drop(ctx context.Context, key string) error {
	at, err := c.key(key)
	if err != nil {
		return err
	}

	if err := c.store.Delete(ctx, at); err != nil {
		return err
	}
	c.forget(key)
	return nil
}

func (c *Cache) forget(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.index[key]; ok {
		c.used -= e.size
		delete(c.index, key)
	}
}

// Used is what this node holds for this cache, in bytes.
func (c *Cache) Used() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used
}

func (c *Cache) expired(at time.Time) bool {
	return !at.IsZero() && !c.now().Before(at)
}

// sizeOf counts the key as well as the value, because a cache full of long
// keys and empty values is still a cache full.
func sizeOf(key string, value []byte) int64 {
	return int64(len(key) + len(value))
}

func encode(written, expires time.Time, value []byte) []byte {
	out := make([]byte, headerSize+len(value))
	binary.BigEndian.PutUint64(out[0:8], uint64(written.UnixNano()))
	if !expires.IsZero() {
		binary.BigEndian.PutUint64(out[8:16], uint64(expires.UnixNano()))
	}
	copy(out[headerSize:], value)
	return out
}

func decode(raw []byte) (written, expires time.Time, value []byte) {
	written = time.Unix(0, int64(binary.BigEndian.Uint64(raw[0:8])))
	if at := int64(binary.BigEndian.Uint64(raw[8:16])); at != 0 {
		expires = time.Unix(0, at)
	}
	return written, expires, raw[headerSize:]
}

// key addresses a guest-supplied key under this cache's prefix, refusing one whose
// join escapes it. Every method addresses entries through it; see pkg/storekey.
func (c *Cache) key(k string) (string, error) {
	if err := storekey.Check(c.prefix, k); err != nil {
		return "", err
	}
	return c.prefix + k, nil
}
