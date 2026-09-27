// Package cache backs `taubyte:cache/store` on this node.
//
// # Why it is its own package
//
// It is an ADAPTER: it sits between the wasm tier, which knows four methods and
// two sentinels, and pkg/cache, which knows eviction and lifetimes and nothing
// about a guest. The composition root is the only place that may NAME both —
// but naming them and implementing the adapter are different jobs, and
// conflating them is how node/ grew to eight thousand lines with twenty-two of
// these in one namespace.
//
// So the root hands this one thing it can ask: somewhere to put rows.
// Everything this package needs is in its own signature, which is the property a
// type taking *Node does not have — that one can reach anything the node has, so
// nothing about it says what it actually depends on, and it cannot be built
// anywhere except beside the node.
//
// # One database for every cache this node serves
//
// A kvdb per cache would be a CRDT, a broadcaster and a pubsub topic per
// resource — dozens on a node running a handful of projects — and every one of
// them would have to be opened before the first request that touched it. One
// database, with the handle as the key prefix, is the same isolation with one
// set of moving parts: pkg/cache appends "/" to the prefix so a cache called
// QmA cannot see QmAB's.
//
// # An open cache is kept, because seeding is not free
//
// pkg/cache scans what this node already holds when it opens, which is what
// makes its ceiling exact rather than optimistic. That scan is bounded by the
// declared size — it reads at most `size` bytes — but paying it per invocation
// would put it on the request path. So an opened cache is remembered by handle
// and reused, which is safe because a cache is a value over a store and the
// store is what replicates.
//
// # What a redeploy does to it
//
// Nothing, and that is deliberate. A cache's DATA survives a deploy; only its
// declared size and lifetime can change, and those are read from the grant when
// it is first opened. A resource whose ttl an author shortened keeps serving
// the old one on a node that already had it open — the vocabulary calls a ttl
// "a floor, not a guarantee" and this is one of the ways it is not one.
package cache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	pkgcache "github.com/taubyte/tau/pkg/cache"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
)

// Stores opens the one database every cache on this node shares.
//
// ONE METHOD AND NOT kvdb.Factory, whose New also takes a logger and a
// rebroadcast interval — a node's decisions about a store, which a cache holds
// no opinion about — and whose result has to be registered with the closers the
// root keeps. What is left here is the only part a cache needs, and it is asked
// for once, lazily, so a node that serves no cache opens no database.
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

// New wires the adapter.
//
// stores MAY NOT BE NIL, because a cache with nowhere to put rows is a cache
// that fails every request — and the root has something to hand over even on a
// node whose operator gave it no database factory: that node's store refuses,
// by name, the first time a workload actually reaches a cache.
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

// translated is the adapter's other half: it turns pkg/cache's sentinels into
// the tier's.
//
// THE TIER MUST NOT IMPORT pkg/cache. Its seam is four methods and two
// sentinels, so any backing can satisfy it — a mushql-backed one, a cloud
// bucket, an in-process map. Translating here is what keeps that true, and it
// is the ONE thing three passing unit tests could not catch between them: a
// miss that arrived as an unknown error was reported to the guest as
// `unavailable`, so every cache miss read as an outage and an author would
// have written a retry around the thing a cache does most.
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
	settingTTL  = "ttl"
	settingSize = "size"
)

// Settings is every attribute path this adapter reads off a grant.
//
// IT IS EXPORTED SO THE ROOT CAN HOLD IT TO THE VOCABULARY. A renamed attribute
// does not fail here: the key is absent, the read is a zero value, and a
// declared ceiling silently becomes no ceiling at all. node/settings_test.go
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
