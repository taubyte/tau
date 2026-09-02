package frontend

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// ratelimit.go is what stands between a request nobody has proven anything
// about and the rest of this cloud, and it is a PAIR of limits per public
// surface because the two defend different things and neither does the other's
// job.
//
//   - The PER-KEY limit is fairness. One hook, or one credential, however busy
//     or however compromised, cannot spend the whole node. It is keyed by a
//     string a stranger chooses, so its map is bounded, and eviction under
//     pressure hands an evicted key a fresh bucket — which is exactly why it is
//     not the bound that holds.
//   - The GLOBAL limit is the bound that holds. It is one bucket, O(1) memory,
//     unkeyable and therefore un-floodable, and it is what caps the total rate
//     at which invented keys reach the cloud. A per-key limiter alone is
//     defeated by using a new key every time.
//
// # Both surfaces need them, and for one reason
//
// A request is admitted BEFORE anything about it has been proven, because on
// both surfaces the proof itself is what costs. On the webhook it is the hook
// lookup — v1's lookup IS that route's authenticator, so in v1 the round trip
// to auth happens first and there is nothing in front of it. On the four job
// routes it is worse: bearer() establishes only that the Authorization header
// has a shape, and mayTouchJob must then read the job from the backend before
// it can authorize anything, because the job's repository_key IS the subject.
// That read is leader-only and passes through a raft barrier, which
// hashicorp/raft implements as a log entry replicated to quorum — so an
// un-admitted job route hands a stranger one replicated entry per HTTP request.
//
// # What is deliberately NOT bounded here
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
	return keyed(s.hookBuckets, hookID, s.cfg.HookRate, s.now)
}

// allowCaller takes a token from one credential's bucket.
//
// THE KEY IS A DIGEST OF THE CREDENTIAL AND NEVER THE CREDENTIAL. The map
// outlives the request by a refill period, and a bearer token left in a map key
// is a live GitHub credential this process kept after it was done with it —
// reachable from a heap dump, and printed by anything that ever ranges over the
// map to debug it. A digest keys the same caller to the same bucket, which is
// all the limiter needs, and is not a credential if it leaks.
//
// SHA-256 rather than a cheaper hash because the input is chosen by the caller:
// a hash they can find collisions in is a hash they can use to put an unrelated
// caller's requests in their own exhausted bucket.
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
