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

// TestTHEBUCKETREFILLSATTHERATETHEAUTHORWROTE, and is capped at the burst.
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

// TestAZERORATEISNOLIMIT, which is what a resource that wrote no rate_limit
// block means.
func TestAZeroRateIsNoLimit(t *testing.T) {
	b := ratelimit.NewBuckets(at(epoch).Now, 16)
	for range 1000 {
		require.True(t, b.Allow("k", 0, 0))
	}
}

// TestANABSENTBURSTISTHERATE, so `rate: 60` on its own admits 60 at once and
// then refills — rather than admitting one and refusing the rest.
func TestAnAbsentBurstIsTheRate(t *testing.T) {
	b := ratelimit.NewBuckets(at(epoch).Now, 16)
	for range 60 {
		require.True(t, b.Allow("k", 60, 0))
	}
	require.False(t, b.Allow("k", 60, 0))
}

// TestKEYSARESEPARATEANDTHETABLEISBOUNDED. A spray of one-request-per-key
// traffic must not grow a bucket per source forever.
func TestKeysAreSeparateAndTheTableIsBounded(t *testing.T) {
	b := ratelimit.NewBuckets(at(epoch).Now, 2)

	require.True(t, b.Allow("a", 60, 1))
	require.False(t, b.Allow("a", 60, 1))
	require.True(t, b.Allow("b", 60, 1), "another key has its own budget")
	require.True(t, b.Allow("c", 60, 1))

	require.LessOrEqual(t, b.Len(), 2)
}

// TestAGAUGEHOLDSWHATISOPEN, which is the limit a rate cannot express: a
// connection that arrives once and is held for an hour costs one token and the
// whole of its slot.
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

// TestARELEASEISIDEMPOTENT. The caller is a deferred close on a connection
// path where a second close is ordinary, and a gauge that decremented twice
// would leak capacity to whoever connected next.
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

// TestAZEROLIMITISNOLIMIT for a gauge too.
func TestAZeroConcurrencyIsNoLimit(t *testing.T) {
	g := ratelimit.NewGauge()
	for range 1000 {
		_, ok := g.Enter("k", 0)
		require.True(t, ok)
	}
	require.Zero(t, g.Held("k"), "an unlimited key is not counted at all")
}

// TestITISSAFEFROMEVERYGOROUTINE: a proxy admits on the accept goroutine and
// releases on each connection's own.
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
