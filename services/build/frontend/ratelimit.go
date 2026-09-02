package frontend

import (
	"sync"
	"time"
)

// ratelimit.go is what stands between an unauthenticated POST and this cloud's
// connection to auth, and there are TWO limits because they defend different
// things and neither does the other's job.
//
//   - The PER-HOOK limit is fairness. One repository's webhook, however busy or
//     however compromised, cannot spend the whole node. It is keyed by a string
//     a stranger chooses, so its map is bounded, and eviction under pressure
//     hands an evicted hook a fresh bucket — which is exactly why it is not the
//     bound that holds.
//   - The GLOBAL limit is the bound that holds. It is one bucket, O(1) memory,
//     unkeyable and therefore un-floodable, and it is what caps the total rate
//     at which invented hook ids can reach the secret lookup. A per-key limiter
//     alone is defeated by using a new key every time.
//
// Both run BEFORE the hook lookup, which is the only ordering that matters:
// v1's lookup IS the route's authenticator, so in v1 the round trip happens
// first and there is nothing in front of it.
//
// Neither limit is replicated. A cloud with n frontends admits n times this,
// which is the honest reading of a per-process limit and is stated rather than
// implied.

// bucket is a token bucket that refills continuously.
type bucket struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time

	burst   float64
	perSec  float64
	nowFunc func() time.Time
}

// newBucket returns a full bucket, or nil when the rate is "no limit".
func newBucket(r Rate, now func() time.Time) *bucket {
	if r.Burst <= 0 || r.Per <= 0 {
		return nil
	}
	return &bucket{
		tokens:  float64(r.Burst),
		last:    now(),
		burst:   float64(r.Burst),
		perSec:  float64(r.Burst) / r.Per.Seconds(),
		nowFunc: now,
	}
}

// allow takes a token if there is one. A nil bucket is no limit, so a Config
// that asked for none does not need a branch at every call site.
func (b *bucket) allow() bool {
	if b == nil {
		return true
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.nowFunc()
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.last = now
		b.tokens = min(b.burst, b.tokens+elapsed.Seconds()*b.perSec)
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// allowHook takes a token from one hook id's bucket.
//
// The buckets live in the same bounded, expiring map the secrets do, with a TTL
// of one refill period — which is not a shortcut but the correct lifetime: a
// bucket untouched for a whole period has refilled to full, so forgetting it
// and minting a fresh one are the same thing.
func (s *Service) allowHook(hookID string) bool {
	b, _ := s.buckets.do(hookID, func() (*bucket, error) {
		return newBucket(s.cfg.HookRate, s.now), nil
	})
	return b.allow()
}
