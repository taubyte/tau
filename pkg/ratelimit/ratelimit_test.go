package ratelimit_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/ratelimit"
)

type clock struct {
	mu sync.Mutex
	at time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(d)
	c.mu.Unlock()
}

func at(t time.Time) *clock { return &clock{at: t} }

var epoch = time.Unix(1_700_000_000, 0)

func TestABucketRefillsAtItsRateAndIsCappedAtItsBurst(t *testing.T) {
	c := at(epoch)
	b := ratelimit.NewBuckets(c.Now, 16)

	require.True(t, b.Allow("k", 60, 3))
	require.True(t, b.Allow("k", 60, 3))
	require.True(t, b.Allow("k", 60, 3))
	require.False(t, b.Allow("k", 60, 3), "the burst is spent")

	c.advance(time.Second)
	require.True(t, b.Allow("k", 60, 3), "one per second at 60 a minute")
	require.False(t, b.Allow("k", 60, 3))

	c.advance(time.Hour)
	require.True(t, b.Allow("k", 60, 3))
	require.True(t, b.Allow("k", 60, 3))
	require.True(t, b.Allow("k", 60, 3))
	require.False(t, b.Allow("k", 60, 3), "an hour idle still only buys the burst")
}

func TestAZeroRateIsNoLimit(t *testing.T) {
	b := ratelimit.NewBuckets(at(epoch).Now, 16)
	for range 1000 {
		require.True(t, b.Allow("k", 0, 0))
	}
}

func TestAnAbsentBurstIsTheRate(t *testing.T) {
	b := ratelimit.NewBuckets(at(epoch).Now, 16)
	for range 60 {
		require.True(t, b.Allow("k", 60, 0))
	}
	require.False(t, b.Allow("k", 60, 0))
}

func TestKeysAreSeparateAndTheTableIsBounded(t *testing.T) {
	b := ratelimit.NewBuckets(at(epoch).Now, 2)

	require.True(t, b.Allow("a", 60, 1))
	require.False(t, b.Allow("a", 60, 1))
	require.True(t, b.Allow("b", 60, 1), "another key has its own budget")
	require.True(t, b.Allow("c", 60, 1))

	require.LessOrEqual(t, b.Len(), 2)
}

func TestAGaugeCountsWhatIsOpenRatherThanWhatArrived(t *testing.T) {
	g := ratelimit.NewGauge()

	first, ok := g.Enter("k", 2)
	require.True(t, ok)
	second, ok := g.Enter("k", 2)
	require.True(t, ok)
	require.Equal(t, 2, g.Held("k"))

	_, ok = g.Enter("k", 2)
	require.False(t, ok, "the third is refused however long ago the first arrived")

	first()
	require.Equal(t, 1, g.Held("k"))

	third, ok := g.Enter("k", 2)
	require.True(t, ok, "and a slot given back is a slot available")

	second()
	third()
	require.Zero(t, g.Held("k"), "an empty key is forgotten rather than kept at zero")
}

func TestReleasingTwiceDoesNotLeakCapacity(t *testing.T) {
	g := ratelimit.NewGauge()

	one, ok := g.Enter("k", 1)
	require.True(t, ok)
	two, ok := g.Enter("k", 1)
	require.False(t, ok)
	require.Nil(t, two, "a refusal hands back no release")

	one()
	one()
	require.Zero(t, g.Held("k"))

	// And the ceiling still holds afterwards.
	_, ok = g.Enter("k", 1)
	require.True(t, ok)
	_, ok = g.Enter("k", 1)
	require.False(t, ok)
}

func TestAZeroConcurrencyIsNoLimit(t *testing.T) {
	g := ratelimit.NewGauge()
	for range 1000 {
		_, ok := g.Enter("k", 0)
		require.True(t, ok)
	}
	require.Zero(t, g.Held("k"), "an unlimited key is not counted at all")
}

func TestBothAreSafeUnderConcurrency(t *testing.T) {
	b := ratelimit.NewBuckets(at(epoch).Now, 64)
	g := ratelimit.NewGauge()

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := string(rune('a' + i%5))
			b.Allow(key, 600, 10)
			if leave, ok := g.Enter(key, 4); ok {
				leave()
			}
		}()
	}
	wg.Wait()

	for _, key := range []string{"a", "b", "c", "d", "e"} {
		require.Zero(t, g.Held(key))
	}
}

func TestALimitedCallerIsNotTheOneEvicted(t *testing.T) {
	now := time.Unix(0, 0)
	l := ratelimit.NewBuckets(func() time.Time { return now }, 3)

	for i := range 2 {
		require.True(t, l.Allow("heavy", 2, 2), "attempt %d was within the budget", i)
	}
	require.False(t, l.Allow("heavy", 2, 2), "the third attempt is over it")

	// A spray of one-request sources, more than the table holds. The heavy caller keeps
	// trying throughout, so it is never the least recently used.
	for _, key := range []string{"spray-1", "spray-2", "spray-3", "spray-4", "spray-5"} {
		l.Allow(key, 2, 2)
		require.False(t, l.Allow("heavy", 2, 2), "the heavy caller got its budget back after %s", key)
	}

	require.Equal(t, 3, l.Len(), "and the table is still bounded")

	// What eviction reached was idle and had refilled anyway.
	require.True(t, l.Allow("spray-1", 2, 2))
}

func TestTimePassingStillRefills(t *testing.T) {
	now := time.Unix(0, 0)
	l := ratelimit.NewBuckets(func() time.Time { return now }, 8)

	require.True(t, l.Allow("caller", 60, 1))
	require.False(t, l.Allow("caller", 60, 1), "the burst is one")

	now = now.Add(time.Second)
	require.True(t, l.Allow("caller", 60, 1), "a second is one token at 60/minute")
}
