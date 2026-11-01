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

func TestNoRateIsNoBucketAndNoLimit(t *testing.T) {
	for _, r := range []Rate{{}, {Burst: 0, Per: time.Minute}, {Burst: 5, Per: 0}} {
		b := newBucket(r, time.Now)
		require.Nil(t, b)
		for range 100 {
			require.True(t, b.allow())
		}
	}
}

func TestTimeGoingBackwardsDoesNotRefill(t *testing.T) {
	c := &clock{at: time.Unix(1000, 0)}
	b := newBucket(Rate{Burst: 1, Per: time.Second}, c.now)

	require.True(t, b.allow())
	c.advance(-time.Hour)
	require.False(t, b.allow())
}

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

func TestBucketsAreKeptApartByCredential(t *testing.T) {
	h := newHarness(t, Config{CallerRate: Rate{Burst: 1, Per: time.Minute}})

	require.True(t, h.allowCaller(testToken))
	require.False(t, h.allowCaller(testToken))
	require.True(t, h.allowCaller("another-token"))

	h.clock.advance(time.Minute)
	require.True(t, h.allowCaller(testToken))
}

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
