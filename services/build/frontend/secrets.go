package frontend

import (
	"context"
	"sync"
	"time"

	"connectrpc.com/connect"

	authv1 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v1"
)

// secrets.go holds auth's answer about a hook for a little while, and the
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
// send. So the cache is bounded, it caches misses as well as hits, and
// ratelimit.go bounds what reaches it in the first place.

// cache is a bounded, expiring map of one fetch's outcome. Entries leave by
// expiry or, when the map is full, oldest-inserted-first.
type cache[V any] struct {
	max int
	ttl time.Duration
	now func() time.Time
	// retain decides whether a FAILED fetch is worth remembering. Only a
	// terminal answer is; see s.hook.
	retain func(error) bool

	mu    sync.Mutex
	items map[string]cacheEntry[V]
	order []string
}

type cacheEntry[V any] struct {
	value V
	err   error
	at    time.Time
}

func newCache[V any](max int, ttl time.Duration, now func() time.Time, retain func(error) bool) *cache[V] {
	return &cache[V]{
		max:    max,
		ttl:    ttl,
		now:    now,
		retain: retain,
		items:  make(map[string]cacheEntry[V], min(max, 64)),
	}
}

// do returns the remembered outcome for key, or calls fetch and remembers it.
//
// FETCH RUNS WITHOUT THE LOCK HELD, deliberately: it is a network call, and a
// cache that serialized every webhook behind one mutex would be a better denial
// of service than the one it prevents. The cost is that a cold key hit
// concurrently is fetched more than once, which is a duplicated read of a value
// that does not change — and the rate limiter is what bounds how many.
func (c *cache[V]) do(key string, fetch func() (V, error)) (V, error) {
	if entry, ok := c.load(key); ok {
		return entry.value, entry.err
	}

	value, err := fetch()
	if err == nil || (c.retain != nil && c.retain(err)) {
		c.store(key, cacheEntry[V]{value: value, err: err, at: c.now()})
	}
	return value, err
}

func (c *cache[V]) load(key string) (cacheEntry[V], bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

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

// hook resolves a hook id through the cache.
//
// WHICH FAILURES ARE REMEMBERED IS THE WHOLE OF IT. "No such hook" is a fact
// about this cloud that will not change in the next minute, and remembering it
// is what makes a flood of invented hook ids cost one round trip each instead
// of one per request. "auth is unreachable" is the opposite: remembering it
// would keep this node refusing real pushes for a minute after auth came back.
func (s *Service) hook(ctx context.Context, hookID string) (*authv1.Hook, error) {
	return s.secrets.do(hookID, func() (*authv1.Hook, error) {
		ctx, cancel := context.WithTimeout(ctx, s.cfg.BackendTimeout)
		defer cancel()
		return s.hooks.Get(ctx, hookID)
	})
}

// terminal reports whether an error is an answer rather than a failure to get
// one. Only an answer is worth caching.
func terminal(err error) bool {
	switch connect.CodeOf(err) {
	case connect.CodeNotFound, connect.CodeInvalidArgument, connect.CodeFailedPrecondition, connect.CodePermissionDenied:
		return true
	default:
		return false
	}
}
