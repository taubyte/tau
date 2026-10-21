package frontend

import (
	"context"
	"errors"
	"sync"
	"time"

	"connectrpc.com/connect"
)

// secrets.go holds the cloud's answer about a hook for a little while, and the
// bound on it is a security property rather than a performance one.
//
// v1 makes a p2p round trip to auth for a hook secret on EVERY unauthenticated
// POST to a hook — the lookup is the route's Auth validator, so it runs
// before anything has been proven — with no cache and no limit anywhere. A
// stranger posting garbage therefore spends this node's connection to auth, and
// spends it once per request.
//
// A cache alone would move the problem rather than solve it: a cache keyed by a
// string the stranger chooses grows with the number of DISTINCT strings they
// send. So the cache is bounded, and ratelimit.go bounds what reaches it in the
// first place.
//
// ONLY A USABLE HOOK IS REMEMBERED — found, with a secret. A miss is not: the
// rate limits already bound what a flood of invented ids costs (one lookup per
// admitted request), and a remembered miss is a project whose secret was just
// set refusing its first pushes for a minute on every node but the one it was
// set through. A failure to reach the register or the vault is not either, for
// the same reason in the other direction.
//
// A REMEMBERED HOOK IS RE-ASKED ONCE WHEN IT FAILS TO VERIFY A DELIVERY
// (webhook.go). That is what makes a rotation correct on every frontend, not
// just the one the secret was set through: the old secret here is a guess, and a
// delivery it refuses is the evidence it went stale.

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

// do returns the remembered value for key, or calls fetch and remembers what
// it returns. A failed fetch is never remembered.
//
// FETCH RUNS WITHOUT THE LOCK HELD, deliberately: it is a network call, and a
// cache that serialized every webhook behind one mutex would be a better denial
// of service than the one it prevents. The cost is that a cold key hit
// concurrently is fetched more than once, which is a duplicated read of a value
// that does not change — and the rate limiter is what bounds how many.
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

	// Oldest-inserted-first rather than least-recently-used. LRU would need a
	// write on every read and would still be defeated by enough distinct keys;
	// what actually holds the line is that there IS a ceiling, and that the
	// global rate limit bounds how fast a stranger can push against it.
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

// hookCache is the cache the webhook route reads hooks through: the bounded,
// expiring map above, plus ONE FETCH IN FLIGHT PER PROJECT and an order on
// fetches, because a refresh is a write that must not lose to an older read.
//
// Every fetch takes a ticket from one counter when it starts, and an entry
// carries the ticket of the fetch that produced it. Only the LATEST fetch
// started for a key may write that key, and a newer start, or a drop, makes
// the older one's result its own callers' alone. So an old lookup that comes
// back after a refresh — the case where a slow answer from before a rotation
// would put the old secret back — changes nothing.
//
// THE MUTEX IS NEVER HELD ACROSS A FETCH. A waiter blocks on the flight's
// channel, not on the lock.
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

// get answers from the cache, or joins the fetch in flight for project, or
// starts one.
//
// THE CACHE AND THE FLIGHTS ARE READ UNDER ONE LOCK. Read apart, a flight
// that lands between the two reads is in neither, and the lookup starts a
// second fetch for a value already held.
func (c *hookCache) get(ctx context.Context, project string) (hookLookup, error) {
	return c.await(ctx, project, 0)
}

// refresh asks again for a hook the cache answered with ticket, never answering
// from the cache. A fetch already in flight is joined only when it started
// AFTER that answer was produced; one started before could return the very
// value being doubted.
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

// run is one fetch. ITS CONTEXT OUTLIVES THE REQUEST THAT STARTED IT, since
// other requests may be waiting on it; the fetch is bounded by the caller's
// own timeout (s.hook's), not by whichever request happened to start it.
func (c *hookCache) run(ctx context.Context, project string, f *hookFlight) {
	f.hook, f.err = c.fetch(context.WithoutCancel(ctx), project)

	c.mu.Lock()
	if c.flights[project] == f {
		delete(c.flights, project)
		switch {
		case f.err == nil && f.hook.signed():
			c.storeLocked(project, cacheEntry[hookEntry]{value: hookEntry{hook: f.hook, ticket: f.ticket}, at: c.now()})
		case absent(f.err) || f.err == nil:
			// AN AUTHORITATIVE ABSENCE EVICTS, and so does a hook that came back
			// with no secret: whatever was remembered is no longer the cloud's
			// answer. A failure to ask leaves the entry as it was — and its
			// expiry as it was, so a lookup that keeps failing never extends how
			// long an old secret is believed.
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
	ctx, cancel := context.WithTimeout(ctx, s.cfg.BackendTimeout)
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
