// Package ratelimit counts two different things: a rate (how fast) and a gauge (how
// many at once). A gateway counts requests per minute; a proxy holds connections
// whose count is the resource. Neither has a default.
//
// Both are per node: N edges enforcing `600` admit up to 600N. A shared counter
// would put a round trip before every connection a limit exists to shed.
//
// What a limit is counted per is the caller's; everything here takes a string.
package ratelimit

import (
	"container/list"
	"sync"
	"time"
)

// Buckets is a token bucket per key, refilled at a rate the caller states.
type Buckets struct {
	now func() time.Time

	mu      sync.Mutex
	buckets map[string]*list.Element
	// order is least-recently-used first, so the table is bounded without dropping the
	// bucket doing the work: a refused caller still calls, so it is never the victim,
	// and an evicted idle bucket had refilled to full anyway.
	order *list.List
	max   int
}

// entry is one key's bucket, held by the list so eviction knows its name.
type entry struct {
	key string
	b   bucket
}

type bucket struct {
	tokens float64
	at     time.Time
}

// NewBuckets holds at most max keys, evicting the least recently used.
func NewBuckets(now func() time.Time, max int) *Buckets {
	if now == nil {
		now = time.Now
	}
	if max < 1 {
		max = 1
	}
	return &Buckets{now: now, buckets: map[string]*list.Element{}, order: list.New(), max: max}
}

// Allow spends a token for key, refilling at rate per minute up to burst.
//
// A ZERO RATE IS NO LIMIT, which is what a resource that wrote no rate_limit
// block means.
func (l *Buckets) Allow(key string, rate, burst int) bool {
	if rate <= 0 {
		return true
	}
	if burst <= 0 {
		burst = rate
	}

	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	el, ok := l.buckets[key]
	if ok {
		l.order.MoveToBack(el)
	} else {
		el = l.order.PushBack(&entry{key: key, b: bucket{tokens: float64(burst), at: now}})
		l.buckets[key] = el
		for l.order.Len() > l.max {
			oldest := l.order.Front()
			delete(l.buckets, oldest.Value.(*entry).key)
			l.order.Remove(oldest)
		}
	}
	b := &el.Value.(*entry).b

	b.tokens += now.Sub(b.at).Minutes() * float64(rate)
	if b.tokens > float64(burst) {
		b.tokens = float64(burst)
	}
	b.at = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Len is how many keys are held.
func (l *Buckets) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// Gauge is how many of something are open at once, per key. An entry exists only
// while something is open under it, so it needs no eviction.
type Gauge struct {
	mu   sync.Mutex
	open map[string]int
}

func NewGauge() *Gauge { return &Gauge{open: map[string]int{}} }

// Enter takes a slot for key and hands back the release. A zero or negative limit is
// no limit. The release is idempotent: a deferred close may run twice.
func (g *Gauge) Enter(key string, limit int) (leave func(), ok bool) {
	if limit <= 0 {
		return func() {}, true
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.open[key] >= limit {
		return nil, false
	}
	g.open[key]++

	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			if g.open[key] <= 1 {
				delete(g.open, key)
				return
			}
			g.open[key]--
		})
	}, true
}

// Held is how many are open under key.
func (g *Gauge) Held(key string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.open[key]
}
