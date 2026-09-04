package frontend

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestABucketSpendsItsBurstAndRefillsOnTheClock(t *testing.T) {
	c := &clock{at: time.Unix(0, 0).UTC()}
	b := newBucket(Rate{Burst: 3, Per: time.Minute}, c.now)

	require.True(t, b.allow())
	require.True(t, b.allow())
	require.True(t, b.allow())
	require.False(t, b.allow())

	// A third of a period is a third of the burst: one token.
	c.advance(20 * time.Second)
	require.True(t, b.allow())
	require.False(t, b.allow())

	// And it never fills past the burst, however long it idles.
	c.advance(time.Hour)
	require.True(t, b.allow())
	require.True(t, b.allow())
	require.True(t, b.allow())
	require.False(t, b.allow())
}

// A Config that asked for no limit gets none, and the call sites do not need a
// branch to say so.
func TestNoRateIsNoBucketAndNoLimit(t *testing.T) {
	for _, r := range []Rate{{}, {Burst: 0, Per: time.Minute}, {Burst: 5, Per: 0}} {
		b := newBucket(r, time.Now)
		require.Nil(t, b)
		for range 100 {
			require.True(t, b.allow())
		}
	}
}

// A clock that goes backwards must not mint tokens.
func TestTimeGoingBackwardsDoesNotRefill(t *testing.T) {
	c := &clock{at: time.Unix(1000, 0)}
	b := newBucket(Rate{Burst: 1, Per: time.Second}, c.now)

	require.True(t, b.allow())
	c.advance(-time.Hour)
	require.False(t, b.allow())
}

// The per-hook buckets live in the same bounded map the secrets do, with a TTL
// of one refill period — a bucket untouched for that long has refilled to full,
// so forgetting it and minting a new one are the same thing.
func TestAForgottenBucketIsAFullBucket(t *testing.T) {
	h := newHarness(t, Config{HookRate: Rate{Burst: 1, Per: time.Minute}})

	require.True(t, h.allowHook("a"))
	require.False(t, h.allowHook("a"))

	h.clock.advance(time.Minute)
	require.True(t, h.allowHook("a"))
}

func TestBucketsAreKeptApartByHook(t *testing.T) {
	h := newHarness(t, Config{HookRate: Rate{Burst: 1, Per: time.Minute}})

	require.True(t, h.allowHook("a"))
	require.False(t, h.allowHook("a"))
	require.True(t, h.allowHook("b"))
}

// The per-hook map has a ceiling, and eviction under pressure hands an evicted
// hook a fresh bucket. That is the approximation ratelimit.go names, and it is
// stated as a test so nobody discovers it as a surprise.
func TestThePerHookLimiterIsDefeatedByEnoughDistinctKeys(t *testing.T) {
	h := newHarness(t, Config{
		SecretCacheMax: 2,
		HookRate:       Rate{Burst: 1, Per: time.Minute},
	})

	require.True(t, h.allowHook("victim"))
	require.False(t, h.allowHook("victim"))

	// Two other ids push the victim's bucket out of a two-entry map.
	require.True(t, h.allowHook("x"))
	require.True(t, h.allowHook("y"))

	require.True(t, h.allowHook("victim"), "the evicted bucket came back full")
}

// A bearer token is a live GitHub credential. The per-caller bucket map outlives
// the request that made it by a whole refill period, so the token must never be
// the key: what goes in is a digest, which keys the same caller to the same
// bucket without keeping the credential around to be read out of a heap dump or
// printed by anything that ranges over the map.
func TestACredentialNeverBecomesAMapKey(t *testing.T) {
	h := newHarness(t, Config{CallerRate: Rate{Burst: 1, Per: time.Minute}})

	require.True(t, h.allowCaller(testToken))

	h.callerBuckets.mu.Lock()
	keys := append([]string(nil), h.callerBuckets.order...)
	h.callerBuckets.mu.Unlock()

	require.Len(t, keys, 1)
	require.NotEqual(t, testToken, keys[0])
	require.NotContains(t, keys[0], testToken)
	// hex over SHA-256: 64 characters, and nothing in it came back out.
	require.Len(t, keys[0], hex.EncodedLen(sha256.Size))
	digest := sha256.Sum256([]byte(testToken))
	require.Equal(t, hex.EncodeToString(digest[:]), keys[0])
}

// The same caller finds the same bucket; a different one gets its own.
func TestBucketsAreKeptApartByCredential(t *testing.T) {
	h := newHarness(t, Config{CallerRate: Rate{Burst: 1, Per: time.Minute}})

	require.True(t, h.allowCaller(testToken))
	require.False(t, h.allowCaller(testToken))
	require.True(t, h.allowCaller("another-token"))

	h.clock.advance(time.Minute)
	require.True(t, h.allowCaller(testToken))
}

// The two keyed maps are separate objects on purpose: one shared map would let
// a flood of invented keys on either surface evict the other's buckets, and an
// evicted bucket comes back full — the per-key limit turned off by a caller
// rather than by an operator.
func TestAFloodOfHookIdsDoesNotEvictACallersBucket(t *testing.T) {
	h := newHarness(t, Config{
		SecretCacheMax: 2,
		HookRate:       Rate{Burst: 1, Per: time.Minute},
		CallerRate:     Rate{Burst: 1, Per: time.Minute},
	})

	require.True(t, h.allowCaller(testToken))
	require.False(t, h.allowCaller(testToken))

	// Enough hook ids to turn over a two-entry map several times.
	for i := range 10 {
		h.allowHook(fmt.Sprintf("invented-%d", i))
	}

	require.False(t, h.allowCaller(testToken), "the caller's spent bucket came back full")
}
