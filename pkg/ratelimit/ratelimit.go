// Package ratelimit counts two different things, because a rate and a
// concurrency are two different limits and neither substitutes for the other.
//
// # A rate is how fast, a gauge is how many at once
//
// A gateway counts REQUESTS PER MINUTE, and that is the right question for
// something that arrives, is answered and goes. A proxy carries connections
// that are held — a database client pool, a subscriber — where the arrival rate
// sits near zero and the COUNT is the resource. The vocabulary asks for both on
// a proxy for exactly that reason, and neither has a default: a ceiling nobody
// chose is a ceiling nobody can defend.
//
// # Both are per node, and that is a real limit rather than a detail
//
// A fleet of N edges enforcing `600` each admits up to 600N, so the number an
// author writes is a per-node ceiling and not a fleet-wide one. A shared counter
// would put a round trip in front of every connection in the system to enforce a
// limit whose whole purpose is to shed load, which is the wrong trade at the
// edge. Say so rather than let it be discovered.
//
// # It counts, and it does not decide what a key is
//
// What a limit is counted PER — an address, a subnet, a header — is the
// caller's vocabulary, and a package that had an opinion would be deciding for
// every caller at once. Everything here takes a string.
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
	// order is least-recently-used first, so the table cannot grow a bucket per
	// source forever AND the bound never drops the bucket doing the work.
	//
	// IT USED TO BE INSERTION ORDER, which is blind to use — and a caller over
	// its budget is the one bucket worth keeping. Under a spray of
	// one-request-per-key traffic, which is the exact traffic this bound exists
	// for, the first-inserted entries are the established callers: `max` new
	// sources evicted every one of them, and each came back with a full burst.
	// The limit stopped holding under load, which is when it is the only thing
	// holding.
	//
	// A caller being refused still calls, so it is the most recently used and is
	// never the victim. What eviction reaches instead is idle, and an idle
	// bucket has refilled to full anyway — so dropping it costs nothing, which
	// is the whole argument for spending a list here.
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

// Len is how many keys are held. It exists for a test of the bound and for an
// operator asking how big the table has grown.
func (l *Buckets) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// Gauge is how many of something are open at once, per key.
//
// IT NEEDS NO EVICTION, unlike Buckets, and the difference is what is being
// held. A bucket has to outlive the request it counted or the next one refills
// from full; a gauge entry exists only while something is open under it, so the
// map is bounded by what is actually in flight rather than by who has ever
// connected.
type Gauge struct {
	mu   sync.Mutex
	open map[string]int
}

func NewGauge() *Gauge { return &Gauge{open: map[string]int{}} }

// Enter takes a slot for key and hands back the release. A zero or negative
// limit is no limit.
//
// THE RELEASE IS IDEMPOTENT, because the caller is a deferred close on a
// connection path where a second close is ordinary — and a gauge that
// decremented twice would leak capacity to whoever connected next.
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

// Held is how many are open under key. It exists for a test and for an operator
// asking why a source is being refused.
func (g *Gauge) Held(key string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.open[key]
}
