package storage

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPackAndUnpackRoundTrip(t *testing.T) {
	for _, c := range []struct {
		ms  uint64
		log uint16
	}{{0, 0}, {1, 1}, {1_700_000_000_000, 0}, {1_700_000_000_000, 65535}, {stampMaxPhysical, 65535}} {
		s, err := PackStamp(c.ms, c.log)
		require.NoError(t, err)
		ms, log := UnpackStamp(s)
		require.Equal(t, c.ms, ms)
		require.Equal(t, c.log, log)
	}

	_, err := PackStamp(stampMaxPhysical+1, 0)
	require.ErrorIs(t, err, ErrStampRange)

	// Packing is monotone in the pair, which is what makes byte order over the
	// stamp segment mean "later".
	a, err := PackStamp(1000, 5)
	require.NoError(t, err)
	b, err := PackStamp(1000, 6)
	require.NoError(t, err)
	c, err := PackStamp(1001, 0)
	require.NoError(t, err)
	require.Less(t, a, b)
	require.Less(t, b, c)
}

func TestStampsAreStrictlyIncreasingWithinAMillisecond(t *testing.T) {
	clk := newFixedClock(1_700_000_000_000)
	c := NewClock(clk.now, time.Minute)

	var last uint64
	for i := 0; i < 1000; i++ {
		s, err := c.Now()
		require.NoError(t, err)
		require.Greater(t, s, last, "a stamp must never repeat or go backwards")
		last = s
	}

	// The physical part has not moved; the logical counter carried it.
	ms, logical := UnpackStamp(last)
	require.Equal(t, uint64(1_700_000_000_000), ms)
	require.Equal(t, uint16(999), logical)
}

func TestTheClockAdvancesWithTheWallClockAndResetsTheCounter(t *testing.T) {
	clk := newFixedClock(1_700_000_000_000)
	c := NewClock(clk.now, time.Minute)

	first, err := c.Now()
	require.NoError(t, err)
	clk.advance(time.Second)
	second, err := c.Now()
	require.NoError(t, err)

	require.Greater(t, second, first)
	ms, logical := UnpackStamp(second)
	require.Equal(t, uint64(1_700_000_001_000), ms)
	require.Equal(t, uint16(0), logical)
}

func TestTheLogicalCounterRefusesRatherThanWraps(t *testing.T) {
	// Wrapping would mint a stamp BELOW one already handed to a caller, so a
	// later write would lose to its own predecessor on every replica at once.
	clk := newFixedClock(1_700_000_000_000)
	c := NewClock(clk.now, time.Minute)

	var last uint64
	for i := 0; i <= stampMaxLogical; i++ {
		s, err := c.Now()
		require.NoError(t, err)
		require.Greater(t, s, last)
		last = s
	}
	_, err := c.Now()
	require.ErrorIs(t, err, ErrLogicalOverflow)

	// And it recovers the instant the millisecond does.
	clk.advance(time.Millisecond)
	s, err := c.Now()
	require.NoError(t, err)
	require.Greater(t, s, last)
}

func TestAWriterThatSawAVersionStampsStrictlyGreater(t *testing.T) {
	clk := newFixedClock(1_700_000_000_000)
	c := NewClock(clk.now, 5*time.Minute)

	// A peer's stamp, from a clock two seconds ahead but well inside the
	// budget.
	remote, err := PackStamp(1_700_000_002_000, 7)
	require.NoError(t, err)
	require.NoError(t, c.Observe(remote))

	mine, err := c.Now()
	require.NoError(t, err)
	require.Greater(t, mine, remote, "having seen it, this node stamps after it")

	// Observing the same stamp again changes nothing, and observing an older
	// one does not drag the clock backwards.
	before, _ := c.Peek()
	require.NoError(t, c.Observe(remote))
	older, err := PackStamp(1_600_000_000_000, 0)
	require.NoError(t, err)
	require.NoError(t, c.Observe(older))
	after, _ := c.Peek()
	require.Equal(t, before, after)

	next, err := c.Now()
	require.NoError(t, err)
	require.Greater(t, next, mine)
}

func TestObserveTakesTheHigherLogicalWithinOneMillisecond(t *testing.T) {
	clk := newFixedClock(1_700_000_000_000)
	c := NewClock(clk.now, time.Minute)

	_, err := c.Now()
	require.NoError(t, err)

	same, err := PackStamp(1_700_000_000_000, 500)
	require.NoError(t, err)
	require.NoError(t, c.Observe(same))
	_, logical := c.Peek()
	require.Equal(t, uint16(500), logical)

	next, err := c.Now()
	require.NoError(t, err)
	require.Greater(t, next, same)

	// A lower logical in the same millisecond does not lower the counter.
	lower, err := PackStamp(1_700_000_000_000, 3)
	require.NoError(t, err)
	require.NoError(t, c.Observe(lower))
	_, logical = c.Peek()
	require.Equal(t, uint16(501), logical)
}

func TestASkewedPeerDoesNotDragThisClock(t *testing.T) {
	clk := newFixedClock(1_700_000_000_000)
	c := NewClock(clk.now, 5*time.Minute)

	_, err := c.Now()
	require.NoError(t, err)
	before, _ := c.Peek()

	// A year ahead.
	insane, err := PackStamp(1_700_000_000_000+365*24*3600*1000, 0)
	require.NoError(t, err)
	err = c.Observe(insane)
	require.ErrorIs(t, err, ErrRemoteClockSkew)

	after, _ := c.Peek()
	require.Equal(t, before, after, "the clock did not move")

	// And this node keeps minting ordinary stamps, because refusing to advance
	// is not refusing to work. THE RECORD IS STILL MERGED BY THE CALLER — this
	// door only decides whether the local clock follows it.
	s, err := c.Now()
	require.NoError(t, err)
	ms, _ := UnpackStamp(s)
	require.Equal(t, uint64(1_700_000_000_000), ms)

	// Exactly at the budget is accepted; one millisecond past it is not.
	atBudget, err := PackStamp(1_700_000_000_000+5*60*1000, 0)
	require.NoError(t, err)
	require.NoError(t, c.Observe(atBudget))

	pastBudget, err := PackStamp(1_700_000_000_000+5*60*1000+1, 0)
	require.NoError(t, err)
	require.ErrorIs(t, c.Observe(pastBudget), ErrRemoteClockSkew)
}

func TestANodeWhoseOwnClockWentBackwardsRefusesToMint(t *testing.T) {
	clk := newFixedClock(1_700_000_000_000)
	c := NewClock(clk.now, 5*time.Minute)

	first, err := c.Now()
	require.NoError(t, err)

	// A step backwards inside the budget is tolerated: the clock does not go
	// backwards, it stalls on the logical counter.
	clk.set(1_700_000_000_000 - 60*1000)
	s, err := c.Now()
	require.NoError(t, err)
	require.Greater(t, s, first)

	// Past the budget it refuses, rather than minting a stamp below one it
	// already handed out.
	clk.set(1_700_000_000_000 - 6*60*1000)
	_, err = c.Now()
	require.ErrorIs(t, err, ErrLocalClockSkew)

	// And it recovers on its own when the clock does. No operator step.
	clk.set(1_700_000_000_000)
	s2, err := c.Now()
	require.NoError(t, err)
	require.Greater(t, s2, s)
}

func TestAClockBeforeTheEpochOrBeyondTheRangeIsRefused(t *testing.T) {
	clk := newFixedClock(-1)
	c := NewClock(clk.now, time.Minute)
	_, err := c.Now()
	require.ErrorIs(t, err, ErrStampRange)
	require.ErrorIs(t, c.Observe(0), ErrStampRange)

	clk.set(int64(stampMaxPhysical) + 1)
	_, err = c.Now()
	require.ErrorIs(t, err, ErrStampRange)
}

func TestNewClockDefaults(t *testing.T) {
	c := NewClock(nil, 0)
	require.Equal(t, DefaultMaxSkew, c.maxSkew)
	s, err := c.Now()
	require.NoError(t, err)
	require.NotZero(t, s)
}

func TestConcurrentMintingNeverRepeatsAStamp(t *testing.T) {
	c := NewClock(time.Now, time.Minute)

	var mu sync.Mutex
	seen := map[uint64]struct{}{}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				s, err := c.Now()
				require.NoError(t, err)
				mu.Lock()
				_, dup := seen[s]
				require.False(t, dup, "stamp %d minted twice", s)
				seen[s] = struct{}{}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	require.Len(t, seen, 4000)
}

func TestTwoNodesInOneMillisecondMintOneStampAndStillProduceTwoRows(t *testing.T) {
	clk := newFixedClock(1_700_000_000_000)
	a := NewClock(clk.now, time.Minute)
	b := NewClock(clk.now, time.Minute)

	sa, err := a.Now()
	require.NoError(t, err)
	sb, err := b.Now()
	require.NoError(t, err)
	require.Equal(t, sa, sb, "two nodes that have not seen each other mint the same u64")

	addrv := addr(t)
	ka, err := addrv.versionKey("obj", sa, "peerA")
	require.NoError(t, err)
	kb, err := addrv.versionKey("obj", sb, "peerB")
	require.NoError(t, err)
	require.NotEqual(t, ka, kb, "and still write two distinct rows, because the writer is in the key")
}
