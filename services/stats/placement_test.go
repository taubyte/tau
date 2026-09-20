package stats

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
