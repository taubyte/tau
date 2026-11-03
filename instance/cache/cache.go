// Package cache backs `taubyte:cache/store`: an adapter between the wasm tier's four
// methods and two sentinels and pkg/cache's eviction and lifetimes, given only a store
// opener.
//
// One database serves every cache on the node, keyed by handle prefix (pkg/cache
// appends "/" so QmA cannot see QmAB). An opened cache is kept per handle, since
// opening seeds it by scanning the store. A redeploy keeps the open cache: a
// shortened ttl is not seen until reopened, and the vocabulary calls ttl a floor, not
// a guarantee.
package cache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/taubyte/tau/pkg/specs"

	pkgcache "github.com/taubyte/tau/pkg/cache"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
)

// Stores opens the one database every cache on this node shares, lazily, so a node
// serving no cache opens none.
type Stores interface {
	Store() (pkgcache.Store, error)
}

// Caches is the adapter: it opens one store lazily and keeps a cache per
// handle. Construct it with New.
type Caches struct {
	stores Stores

	mu     sync.Mutex
	store  pkgcache.Store
	opened map[string]*pkgcache.Cache
}

var _ wasmtier.Caches = (*Caches)(nil)

// New wires the adapter. stores may not be nil; a node with no database factory still
// hands one over, which refuses by name on first use.
func New(stores Stores) (*Caches, error) {
	if stores == nil {
		return nil, errors.New("cache: no store to keep a cache in")
	}
	return &Caches{stores: stores}, nil
}

// Open resolves one grant into a cache.
func (c *Caches) Open(ctx context.Context, grant invoke.Grant) (wasmtier.Cache, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if held, ok := c.opened[grant.Handle]; ok {
		return translated{held}, nil
	}

	store, err := c.backing()
	if err != nil {
		return nil, err
	}

	opened, err := pkgcache.Open(ctx, store, pkgcache.Config{
		Prefix: grant.Handle,
		TTL:    duration(grant.Settings[settingTTL]),
		Size:   grant.Number(settingSize),
	})
	if err != nil {
		return nil, err
	}

	if c.opened == nil {
		c.opened = map[string]*pkgcache.Cache{}
	}
	c.opened[grant.Handle] = opened
	return translated{opened}, nil
}

// backing asks for the one store, once. It runs under the same lock as the map
// so two concurrent first-requests cannot open two.
func (c *Caches) backing() (pkgcache.Store, error) {
	if c.store != nil {
		return c.store, nil
	}
	store, err := c.stores.Store()
	if err != nil {
		return nil, err
	}
	c.store = store
	return store, nil
}

// translated turns pkg/cache's sentinels into the tier's, so the tier never imports
// pkg/cache and a miss is not reported to a guest as `unavailable`.
type translated struct{ *pkgcache.Cache }

func (t translated) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := t.Cache.Get(ctx, key)
	return value, asTierError(err)
}

func (t translated) Put(ctx context.Context, key string, value []byte) error {
	return asTierError(t.Cache.Put(ctx, key, value))
}

func (t translated) Delete(ctx context.Context, key string) error {
	return asTierError(t.Cache.Delete(ctx, key))
}

func (t translated) List(ctx context.Context, prefix string) ([]string, error) {
	names, err := t.Cache.List(ctx, prefix)
	return names, asTierError(err)
}

// asTierError keeps the original as the wrapped cause, so a log still says
// which key and which cache while the guest sees the arm it can act on.
func asTierError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pkgcache.ErrNotFound):
		return fmt.Errorf("%w: %w", wasmtier.ErrNotFound, err)
	case errors.Is(err, pkgcache.ErrTooLarge):
		return fmt.Errorf("%w: %w", wasmtier.ErrTooLarge, err)
	default:
		return err
	}
}

// The setting keys services/worker writes, which are the vocabulary's own
// attribute paths.
const (
	settingTTL  = specs.CacheKeyTtl
	settingSize = specs.CacheKeySize
)

// Settings is every attribute path this adapter reads off a grant.
//
// IT IS EXPORTED SO THE ROOT CAN HOLD IT TO THE VOCABULARY. A renamed attribute
// does not fail here: the key is absent, the read is a zero value, and a
// declared ceiling silently becomes no ceiling at all. instance/settings_test.go
// walks this list against the schema, and its own doc carries the post-mortem
// that test came out of.
var Settings = []string{settingTTL, settingSize}

// A ZERO OR UNPARSEABLE SETTING IS NO LIMIT rather than a refusal. The
// vocabulary makes `ttl` required, so an absent one means the composer did not
// read it — and a cache with no ceiling is a bug to find in a log, where a
// cache that refuses every write is an outage. `size` is read the same way, by
// [invoke.Grant.Number], which is where that sentence is written down for every
// backing that reads a count off a grant.
func duration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}
