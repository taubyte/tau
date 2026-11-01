package discovery

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/stats"
)

func TestPlacementIsTheStableSetAndNotTheFreshOne(t *testing.T) {
	var mu sync.Mutex
	now := time.Unix(0, 0)
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		now = now.Add(d)
		mu.Unlock()
	}

	s, err := New(newHost(t), Config{Interval: time.Second},
		WithClock(clock), WithBus(&memBus{}), WithReporter(fixedSnapshot(Snapshot{Ready: true})))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	peers := []string{"n1", "n2", "n3"}
	for _, peer := range peers {
		require.NoError(t, s.view.Observe(stats.Report{Peer: peer, Load: 0.1, Ready: true}))
		s.beat(peer, 1, true)
	}

	// A peer that has just appeared is routable but owns nothing yet.
	require.Empty(t, s.Placement(), "data was placed on a peer that had just appeared")

	// Kept reporting, it settles, and only then does it enter the placement set.
	for range 120 {
		advance(time.Second)
		for _, peer := range peers {
			s.beat(peer, 1, true)
		}
	}

	require.Len(t, s.Placement(), 3)

	// A blip does not move anything: one missed interval drops a peer from the fresh
	// view, not the placement set.
	advance(3 * time.Second)
	require.Len(t, s.Placement(), 3, "a blip moved data")
}

func TestTheStabilizeWindowIsTheMinuteFloorUnlessTheConfigSaysOtherwise(t *testing.T) {
	// Unset: the minute, at every interval a fleet would plausibly run.
	for _, interval := range []time.Duration{time.Second, 5 * time.Second, 15 * time.Second} {
		c, err := Config{Interval: interval}.withDefaults()
		require.NoError(t, err)
		require.GreaterOrEqual(t, c.Stabilize, time.Minute,
			"a fleet gossiping every %s settles in under a minute", interval)
	}

	// Above the floor it is four freshness windows, which is what makes a
	// slow-gossiping fleet settle slowly rather than on the same minute.
	c, err := Config{Interval: time.Minute}.withDefaults()
	require.NoError(t, err)
	require.Equal(t, stabilizeFreshWindows*freshIntervals*time.Minute, c.Stabilize)

	// Named: exactly what was named, floor or no floor.
	c, err = Config{Stabilize: 15 * time.Second}.withDefaults()
	require.NoError(t, err)
	require.Equal(t, 15*time.Second, c.Stabilize)
}

func TestAStabilizeShorterThanFreshnessIsRefused(t *testing.T) {
	_, err := New(newHost(t), Config{Interval: time.Second, Stabilize: time.Second},
		WithBus(&memBus{}), WithReporter(fixedSnapshot(Snapshot{Ready: true})))
	require.ErrorContains(t, err, "stabilize")
	require.ErrorContains(t, err, "faster than acknowledgement")
}

func TestAConfiguredStabilizeIsWhatPlacementRunsOn(t *testing.T) {
	var mu sync.Mutex
	now := time.Unix(0, 0)
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}

	s, err := New(newHost(t), Config{Interval: time.Second, Stabilize: 10 * time.Second},
		WithClock(clock), WithBus(&memBus{}), WithReporter(fixedSnapshot(Snapshot{Ready: true})))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	beat := func() {
		require.NoError(t, s.view.Observe(stats.Report{Peer: "eu1", Load: 0.1, Ready: true}))
		s.beat("eu1", 1, true)
	}

	beat()
	for range 9 {
		mu.Lock()
		now = now.Add(time.Second)
		mu.Unlock()
		beat()
	}
	require.Empty(t, s.Placement(), "a peer settled before the window it was given elapsed")

	mu.Lock()
	now = now.Add(time.Second)
	mu.Unlock()
	beat()
	require.Len(t, s.Placement(), 1, "the configured window elapsed and nothing settled")
}
