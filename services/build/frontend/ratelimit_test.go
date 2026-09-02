package frontend

import (
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
