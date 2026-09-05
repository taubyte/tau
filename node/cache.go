package node

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	ds "github.com/ipfs/go-datastore"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/pkg/cache"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
)

// cache.go backs `taubyte:handler/cache` on this node.
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

// caches is the node's Caches: it opens one kvdb lazily and keeps a cache per
// handle.
type caches struct {
	node *Node

	mu     sync.Mutex
	db     kvdb.KVDB
	opened map[string]*cache.Cache
}

var _ wasmtier.Caches = (*caches)(nil)

// ErrNoCacheStore is a node asked for a cache with no database factory. It is
// a composition fault: WithDatabases is what a node is given, and a node
// running a worker without one can run nothing that reaches state.
var ErrNoCacheStore = errors.New("node: no database factory to open a cache with")

// Open resolves one grant into a cache.
func (c *caches) Open(ctx context.Context, grant invoke.Grant) (wasmtier.Cache, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if held, ok := c.opened[grant.Handle]; ok {
		return held, nil
	}

	store, err := c.store()
	if err != nil {
		return nil, err
	}

	opened, err := cache.Open(ctx, store, cache.Config{
		Prefix: grant.Handle,
		TTL:    duration(grant.Settings[cacheTTL]),
		Size:   byteCount(grant.Settings[cacheSize]),
	})
	if err != nil {
		return nil, err
	}

	if c.opened == nil {
		c.opened = map[string]*cache.Cache{}
	}
	c.opened[grant.Handle] = opened
	return opened, nil
}

// The setting keys services/worker writes, which are the vocabulary's own
// attribute paths.
const (
	cacheTTL  = "keys/ttl"
	cacheSize = "keys/size"
)

// store opens the one database, once. It is under the same lock as the map so
// two concurrent first-requests cannot open two.
func (c *caches) store() (cache.Store, error) {
	if c.db != nil {
		return cacheStore{c.db}, nil
	}
	if c.node.opts.databases == nil {
		return nil, ErrNoCacheStore
	}

	db, err := c.node.opts.databases.New(logger, cacheDatabase, rebroadcastSeconds)
	if err != nil {
		return nil, fmt.Errorf("node: opening the cache database: %w", err)
	}
	c.db = db
	c.node.closers = append(c.node.closers, closerFunc(db.Close))
	return cacheStore{db}, nil
}

// cacheDatabase is the one database's name. It is not a service name — no node
// "runs caches" — so it is spelled here rather than taken from the Services
// list.
const cacheDatabase = "caches"

// A ZERO OR UNPARSEABLE SETTING IS NO LIMIT rather than a refusal. The
// vocabulary makes `size` required, so an absent one means the composer did not
// read it — and a cache with no ceiling is a bug to find in a log, where a
// cache that refuses every write is an outage.
func duration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}

func byteCount(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func isNotFound(err error) bool { return errors.Is(err, ds.ErrNotFound) }

// cacheStore adapts a kvdb to what pkg/cache needs — the same four methods
// services/secrets keeps, and the same separation of "not there" from "could
// not look".
type cacheStore struct{ db kvdb.KVDB }

func (s cacheStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	v, err := s.db.Get(ctx, key)
	switch {
	case err == nil:
		return v, true, nil
	case isNotFound(err):
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("node: reading a cache entry: %w", err)
	}
}

func (s cacheStore) Put(ctx context.Context, key string, value []byte) error {
	return s.db.Put(ctx, key, value)
}

func (s cacheStore) Delete(ctx context.Context, key string) error {
	if err := s.db.Delete(ctx, key); err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

func (s cacheStore) List(ctx context.Context, prefix string) ([]string, error) {
	return s.db.List(ctx, prefix)
}
