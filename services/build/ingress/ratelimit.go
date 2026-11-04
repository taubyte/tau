package ingress

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// Two limits per public surface: a per-key limit for fairness (keyed by a
// stranger's string, so bounded and evictable, hence not the bound that holds)
// and a global one, unkeyable, that is the bound that holds. Requests are admitted
// before anything is proven because the proof itself costs: a hook lookup, or a
// job read through a raft barrier. Limits are per process.

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

// allowHook takes a token from one hook id's bucket, kept with a TTL of one refill
// period: a bucket idle that long is full anyway.
func (s *Service) allowHook(hookID string) bool {
	return keyed(s.hookBuckets, hookID, s.cfg.HookRate, s.now)
}

// allowCaller takes a token from one credential's bucket, keyed by its SHA-256 so
// no credential outlives the request in a map; a collision-resistant hash because
// the caller chooses the input.
func (s *Service) allowCaller(token string) bool {
	digest := sha256.Sum256([]byte(token))
	return keyed(s.callerBuckets, hex.EncodeToString(digest[:]), s.cfg.CallerRate, s.now)
}

// keyed is the per-key half of both surfaces: find this key's bucket in the
// bounded map, minting a full one if it has none, and take a token from it.
func keyed(buckets *cache[*bucket], key string, rate Rate, now func() time.Time) bool {
	b, _ := buckets.do(key, func() (*bucket, error) {
		return newBucket(rate, now), nil
	})
	return b.allow()
}
