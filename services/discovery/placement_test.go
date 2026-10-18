package discovery

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/stats"
)

// TestPLACEMENTRUNSONASETTLEDVIEW, not on the fresh one.
//
// Routing wants fresh and placement wants settled, and the difference is what a
// blip costs. A node that stops reporting for one interval should stop
// receiving requests at once — but it must NOT move a database, because moving
// one costs a copy handed over and a copy deleted. A fleet that placed on the
// fresh view would spend its day rebalancing on jitter.
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

	s, err := New(newHost(t), Config{Interval: time.Second, Region: "eu"},
		WithClock(clock), WithBus(&memBus{}), WithReporter(fixedSnapshot(Snapshot{Ready: true})))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	for _, n := range []struct{ peer, region string }{
		{"eu1", "eu"}, {"eu2", "eu"}, {"us1", "us"},
	} {
		require.NoError(t, s.view.Observe(stats.Report{
			Peer: n.peer, Load: 0.1, Ready: true, Region: n.region,
		}))
		s.beat(n.peer, 1, true, n.region)
	}

	// A PEER THAT HAS JUST APPEARED OWNS NOTHING YET. It is routable — it
	// answered — and it is not somewhere to put data.
	require.Empty(t, s.Placement(), "data was placed on a peer that had just appeared")

	// Kept reporting, it settles, and only then does it enter the placement set.
	for range 120 {
		advance(time.Second)
		for _, n := range []struct{ peer, region string }{
			{"eu1", "eu"}, {"eu2", "eu"}, {"us1", "us"},
		} {
			s.beat(n.peer, 1, true, n.region)
		}
	}

	at := s.Placement()
	require.Len(t, at["eu"], 2)
	require.Len(t, at["us"], 1)

	// AND A BLIP DOES NOT MOVE ANYTHING. One missed interval drops a peer out
	// of the fresh view; the placement set does not notice.
	advance(3 * time.Second)
	require.Len(t, s.Placement()["eu"], 2, "a blip moved data")
}

// TestTheStabilizeWindowIsTheMinuteFloorUnlessTheConfigSaysOtherwise.
//
// The floor is a FLEET POLICY and not a property of the mechanism — a cloud
// refuses to move data on anything shorter however fast it gossips — so the
// default has to stay exactly where it was while a caller that has a reason can
// still name a shorter one.
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

// TestAStabilizeShorterThanFreshnessIsRefused, with the operator's own field
// named: pkg/membership refuses it too, but about a liveness window the
// operator never wrote.
func TestAStabilizeShorterThanFreshnessIsRefused(t *testing.T) {
	_, err := New(newHost(t), Config{Interval: time.Second, Stabilize: time.Second},
		WithBus(&memBus{}), WithReporter(fixedSnapshot(Snapshot{Ready: true})))
	require.ErrorContains(t, err, "stabilize")
	require.ErrorContains(t, err, "faster than acknowledgement")
}

// TestAConfiguredStabilizeIsWhatPlacementRunsOn — the seam end to end, because
// a field that defaults correctly and reaches nothing is the failure this test
// exists for.
func TestAConfiguredStabilizeIsWhatPlacementRunsOn(t *testing.T) {
	var mu sync.Mutex
	now := time.Unix(0, 0)
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}

	s, err := New(newHost(t), Config{Interval: time.Second, Stabilize: 10 * time.Second, Region: "eu"},
		WithClock(clock), WithBus(&memBus{}), WithReporter(fixedSnapshot(Snapshot{Ready: true})))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	beat := func() {
		require.NoError(t, s.view.Observe(stats.Report{Peer: "eu1", Load: 0.1, Ready: true, Region: "eu"}))
		s.beat("eu1", 1, true, "eu")
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
	require.Len(t, s.Placement()["eu"], 1, "the configured window elapsed and nothing settled")
}
