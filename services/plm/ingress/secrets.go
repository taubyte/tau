package ingress

import (
	"context"
	"errors"
	"sync"
	"time"

	"connectrpc.com/connect"
)

// secrets.go caches hook lookups, bounded because the key is a stranger's string;
// ratelimit.go bounds what reaches it. Only a usable hook is remembered: a miss or
// a failed lookup is not, so a newly set secret works at once everywhere. A
// remembered hook that fails to verify a delivery is re-asked once (webhook.go),
// which is what makes rotation correct on every plm node.

// cache is a bounded, expiring map of one fetch's outcome. Entries leave by
// expiry or, when the map is full, oldest-inserted-first.
type cache[V any] struct {
	max int
	ttl time.Duration
	now func() time.Time

	mu    sync.Mutex
	items map[string]cacheEntry[V]
	order []string
}

type cacheEntry[V any] struct {
	value V
	at    time.Time
}

func newCache[V any](max int, ttl time.Duration, now func() time.Time) *cache[V] {
	return &cache[V]{
		max:   max,
		ttl:   ttl,
		now:   now,
		items: make(map[string]cacheEntry[V], min(max, 64)),
	}
}

// do returns the remembered value for key, or fetches and remembers it; a failed
// fetch is not remembered. The fetch runs without the lock, so a concurrent cold
// key may be fetched twice.
func (c *cache[V]) do(key string, fetch func() (V, error)) (V, error) {
	if entry, ok := c.load(key); ok {
		return entry.value, nil
	}

	value, err := fetch()
	if err == nil {
		c.store(key, cacheEntry[V]{value: value, at: c.now()})
	}
	return value, err
}

func (c *cache[V]) load(key string) (cacheEntry[V], bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loadLocked(key)
}

func (c *cache[V]) loadLocked(key string) (cacheEntry[V], bool) {
	entry, ok := c.items[key]
	if !ok {
		return cacheEntry[V]{}, false
	}
	if c.now().Sub(entry.at) >= c.ttl {
		delete(c.items, key)
		c.forget(key)
		return cacheEntry[V]{}, false
	}
	return entry, true
}

func (c *cache[V]) store(key string, entry cacheEntry[V]) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.storeLocked(key, entry)
}

func (c *cache[V]) storeLocked(key string, entry cacheEntry[V]) {
	if _, ok := c.items[key]; !ok {
		c.order = append(c.order, key)
	}
	c.items[key] = entry

	// Oldest-inserted-first, not LRU: the ceiling and the global rate limit are what
	// hold.
	for len(c.order) > c.max {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.items, oldest)
	}
}

func (c *cache[V]) forget(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}

// hookCache is the webhook route's cache: one fetch in flight per project, and
// tickets so only the latest fetch for a key may write it; a slow pre-rotation
// answer cannot restore an old secret. The mutex is never held across a fetch.
type hookCache struct {
	*cache[hookEntry]
	fetch func(ctx context.Context, project string) (Hook, error)

	seq     uint64
	flights map[string]*hookFlight
}

type hookEntry struct {
	hook   Hook
	ticket uint64
}

type hookFlight struct {
	ticket uint64
	done   chan struct{}
	hook   Hook
	err    error
}

// hookLookup is one answer and where it came from. cached is what the webhook
// route needs to know: only a remembered hook can be stale.
type hookLookup struct {
	hook   Hook
	cached bool
	ticket uint64
}

func newHookCache(max int, ttl time.Duration, now func() time.Time, fetch func(context.Context, string) (Hook, error)) *hookCache {
	return &hookCache{
		cache:   newCache[hookEntry](max, ttl, now),
		fetch:   fetch,
		flights: make(map[string]*hookFlight),
	}
}

// get answers from the cache, joins the flight for project, or starts one; cache
// and flights are read under one lock.
func (c *hookCache) get(ctx context.Context, project string) (hookLookup, error) {
	return c.await(ctx, project, 0)
}

// refresh asks again for a hook answered with ticket, joining only a fetch started
// after that answer.
func (c *hookCache) refresh(ctx context.Context, project string, ticket uint64) (hookLookup, error) {
	return c.await(ctx, project, ticket+1)
}

func (c *hookCache) await(ctx context.Context, project string, notBefore uint64) (hookLookup, error) {
	c.mu.Lock()
	if notBefore == 0 {
		if entry, ok := c.loadLocked(project); ok {
			c.mu.Unlock()
			return hookLookup{hook: entry.value.hook, cached: true, ticket: entry.value.ticket}, nil
		}
	}
	f, ok := c.flights[project]
	if !ok || f.ticket < notBefore {
		c.seq++
		f = &hookFlight{ticket: c.seq, done: make(chan struct{})}
		c.flights[project] = f
		go c.run(ctx, project, f)
	}
	c.mu.Unlock()

	select {
	case <-f.done:
		return hookLookup{hook: f.hook, ticket: f.ticket}, f.err
	case <-ctx.Done():
		return hookLookup{}, ctx.Err()
	}
}

// run is one fetch, its context outliving the starting request since others may
// wait on it; bounded by s.hook's timeout.
func (c *hookCache) run(ctx context.Context, project string, f *hookFlight) {
	f.hook, f.err = c.fetch(context.WithoutCancel(ctx), project)

	c.mu.Lock()
	if c.flights[project] == f {
		delete(c.flights, project)
		switch {
		case f.err == nil && f.hook.signed():
			c.storeLocked(project, cacheEntry[hookEntry]{value: hookEntry{hook: f.hook, ticket: f.ticket}, at: c.now()})
		case absent(f.err) || f.err == nil:
			// An authoritative absence, or a hook with no secret, evicts. A failure to ask
			// leaves the entry and its expiry as they were.
			delete(c.items, project)
			c.forget(project)
		}
	}
	c.mu.Unlock()
	close(f.done)
}

// drop discards what is held for project and detaches any fetch in flight, so
// that fetch's answer — which may predate whatever made the caller drop — is
// not remembered.
func (c *hookCache) drop(project string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, project)
	c.forget(project)
	delete(c.flights, project)
}

func (s *Service) fetchHook(ctx context.Context, project string) (Hook, error) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.CallTimeout)
	defer cancel()
	return s.hooks.Hook(ctx, project)
}

// absent reports whether an error is the cloud's ANSWER that there is no hook,
// rather than a failure to get an answer. The first is a denial; the second is
// a 503, because this node cannot say either way.
func absent(err error) bool {
	if errors.Is(err, ErrNoHook) {
		return true
	}
	switch connect.CodeOf(err) {
	case connect.CodeNotFound, connect.CodeInvalidArgument, connect.CodeFailedPrecondition, connect.CodePermissionDenied:
		return true
	default:
		return false
	}
}
