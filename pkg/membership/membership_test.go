package membership

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/v2/placement"
)

// clock is the local clock, injected. Every test in this file runs in
// simulated time: there is no sleep anywhere, which is the point of Observe
// taking no timestamp and this package owning no goroutine.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newSet(t *testing.T, c *clock) *Set {
	t.Helper()
	s, err := New(Options{Liveness: 5 * time.Second, Stabilize: 60 * time.Second, Now: c.now})
	require.NoError(t, err)
	return s
}

func ids(ms []Member) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Peer)
	}
	sort.Strings(out)
	return out
}

func TestDefaultsAndTheirRefusals(t *testing.T) {
	s, err := New(Options{})
	require.NoError(t, err)
	require.Equal(t, 5*time.Second, s.opts.Liveness)
	require.Equal(t, 60*time.Second, s.opts.Stabilize)
	require.Equal(t, 600*time.Second, s.opts.Forget)
	require.NotNil(t, s.opts.Now)

	_, err = New(Options{Liveness: 90 * time.Second, Stabilize: 60 * time.Second})
	require.ErrorContains(t, err, "shorter than liveness",
		"placement moving faster than acknowledgement is the thing two timescales exist to prevent")

	_, err = New(Options{Stabilize: 60 * time.Second, Forget: 30 * time.Second})
	require.ErrorContains(t, err, "does not exceed stabilize")

	_, err = New(Options{Stabilize: 60 * time.Second, Forget: 60 * time.Second})
	require.ErrorContains(t, err, "does not exceed stabilize")
}

func TestABeatWithNoSenderIsRefused(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	s := newSet(t, c)
	require.Error(t, s.Observe(Beat{Incarnation: 1, Accepts: true}))
	require.Empty(t, s.All())
}

func TestLiveIsTheAckTimescaleAndStableIsThePlacementTimescale(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	s := newSet(t, c)

	require.NoError(t, s.Observe(Beat{Peer: "A", Incarnation: 1, Accepts: true}))
	require.Equal(t, []string{"A"}, ids(s.Live()), "an ack target the instant it beats")
	require.Empty(t, s.Stable(), "and not a placement target until it has proved it is staying")

	// Beat every second. Stability arrives at exactly 60s of presence, not before.
	for i := 0; i < 59; i++ {
		c.advance(time.Second)
		require.NoError(t, s.Observe(Beat{Peer: "A", Incarnation: 1, Accepts: true}))
		require.Empty(t, s.Stable(), "stable after %ds", i+1)
	}
	c.advance(time.Second)
	require.NoError(t, s.Observe(Beat{Peer: "A", Incarnation: 1, Accepts: true}))
	require.Equal(t, []string{"A"}, ids(s.Stable()))
}

func TestLivenessExpiresFourBeatsAfterTheLast(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	s := newSet(t, c)
	require.NoError(t, s.Observe(Beat{Peer: "A", Incarnation: 1, Accepts: true}))

	c.advance(4999 * time.Millisecond)
	require.Equal(t, []string{"A"}, ids(s.Live()))
	c.advance(time.Millisecond)
	require.Empty(t, s.Live(), "the window is closed AT liveness, not after it")

	m, ok := s.Get("A")
	require.True(t, ok, "not live is not forgotten")
	require.False(t, m.Live)
	require.False(t, m.Stable)
}

// TestANodeThatFlapsForThirtySecondsMovesZeroBytes is the reason this package
// has two timescales at all, from both directions: a newcomer that keeps
// dropping out never earns a place, and an established member that drops out
// and comes back keeps its place. Either failure re-homes a share of the
// fleet's bytes for nothing.
func TestANodeThatFlapsForThirtySecondsMovesZeroBytes(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	s := newSet(t, c)

	// "A" is established: 60s of continuous presence.
	for i := 0; i <= 60; i++ {
		require.NoError(t, s.Observe(Beat{Peer: "A", Incarnation: 1, Accepts: true}))
		c.advance(time.Second)
	}
	require.Equal(t, []string{"A"}, ids(s.Stable()))

	// "B" flaps: 25s present, 30s silent, repeatedly. It never becomes stable.
	// Meanwhile A stays quiet for the same 30s windows and never loses its
	// place, because 30s < stabilize.
	for round := 0; round < 6; round++ {
		for i := 0; i < 25; i++ {
			require.NoError(t, s.Observe(Beat{Peer: "B", Incarnation: 1, Accepts: true}))
			c.advance(time.Second)
		}
		c.advance(30 * time.Second)

		require.NotContains(t, ids(s.Stable()), "B", "a flapping newcomer must never be placed (round %d)", round)
		require.Contains(t, ids(s.Stable()), "A", "an established member must not be unplaced by a 30s gap (round %d)", round)
		require.NotContains(t, ids(s.Live()), "A", "...even though it is not an ack target during the gap")

		// A keeps beating between B's rounds, so its own absence never reaches
		// the stabilize window.
		require.NoError(t, s.Observe(Beat{Peer: "A", Incarnation: 1, Accepts: true}))
	}
}

func TestAnEstablishedMemberLeavesThePlacementSetAfterStabilize(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	s := newSet(t, c)
	for i := 0; i <= 60; i++ {
		require.NoError(t, s.Observe(Beat{Peer: "A", Incarnation: 1, Accepts: true}))
		c.advance(time.Second)
	}
	require.Equal(t, []string{"A"}, ids(s.Stable()))

	// Absence is timed from when the liveness window closed, not from when
	// somebody looked: the place is lost liveness (5s) + stabilize (60s) after
	// the LAST BEAT, which the loop above left one second behind the clock.
	c.advance(63 * time.Second)
	require.Equal(t, []string{"A"}, ids(s.Stable()))
	c.advance(2 * time.Second)
	require.Empty(t, s.Stable())
	require.Empty(t, s.Live())
	_, ok := s.Get("A")
	require.True(t, ok, "unplaced is not forgotten")
}

func TestAbsenceIsTimedFromTheWindowClosingNotFromTheFirstLook(t *testing.T) {
	// The bug this is written against: computing absentSince at read time
	// makes "when did it leave" depend on when somebody happened to call, so
	// a set nobody reads for an hour keeps every dead peer placed.
	c := &clock{t: time.Unix(1000, 0)}
	s := newSet(t, c)
	for i := 0; i <= 60; i++ {
		require.NoError(t, s.Observe(Beat{Peer: "A", Incarnation: 1, Accepts: true}))
		c.advance(time.Second)
	}
	require.Equal(t, []string{"A"}, ids(s.Stable()))

	// Nobody reads the set for the whole absence. One read afterwards must
	// find it unplaced.
	c.advance(66 * time.Second)
	require.Empty(t, s.Stable())
}

func TestAStaleIncarnationIsRefusedAndChangesNothing(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	s := newSet(t, c)
	require.NoError(t, s.Observe(Beat{Peer: "A", Incarnation: 7, Accepts: true}))

	c.advance(4 * time.Second)
	err := s.Observe(Beat{Peer: "A", Incarnation: 6, Accepts: false})
	require.ErrorIs(t, err, ErrStaleIncarnation)

	m, _ := s.Get("A")
	require.Equal(t, uint64(7), m.Incarnation)
	require.True(t, m.Accepts, "a replayed beat must not flip a live declaration")

	// And it did not refresh liveness either.
	c.advance(2 * time.Second)
	require.Empty(t, s.Live(), "a refused beat is not a heartbeat")
}

func TestARestartRefreshesLivenessAndKeepsThePlacement(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	s := newSet(t, c)
	for i := 0; i <= 60; i++ {
		require.NoError(t, s.Observe(Beat{Peer: "A", Incarnation: 1, Accepts: true}))
		c.advance(time.Second)
	}
	require.Equal(t, []string{"A"}, ids(s.Stable()))

	// Down for 10s, back with a higher incarnation.
	c.advance(10 * time.Second)
	require.Empty(t, s.Live())
	require.NoError(t, s.Observe(Beat{Peer: "A", Incarnation: 2, Accepts: true}))

	m, _ := s.Get("A")
	require.True(t, m.Live)
	require.True(t, m.Stable, "a restart is the same node id: HRW gives it the same shards back")
	require.Equal(t, uint64(2), m.Incarnation, "and the restart is visible to a caller that must refill it")
}

func TestEqualIncarnationIsTheOrdinaryBeat(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	s := newSet(t, c)
	require.NoError(t, s.Observe(Beat{Peer: "A", Incarnation: 3, Accepts: true}))
	c.advance(time.Second)
	require.NoError(t, s.Observe(Beat{Peer: "A", Incarnation: 3, Accepts: false}))
	m, _ := s.Get("A")
	require.False(t, m.Accepts, "a decline arrives on an ordinary beat")
	require.True(t, m.Live)
}

func TestEvictIsImmediateAndLocal(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	s := newSet(t, c)
	for i := 0; i <= 60; i++ {
		require.NoError(t, s.Observe(Beat{Peer: "A", Incarnation: 1, Accepts: true}))
		c.advance(time.Second)
	}
	require.Equal(t, []string{"A"}, ids(s.Stable()))

	s.Evict("A")
	require.Empty(t, s.Live())
	require.Empty(t, s.Stable())
	require.Empty(t, s.All())
	_, ok := s.Get("A")
	require.False(t, ok)

	// An evicted peer that beats again is a newcomer and re-earns its place.
	require.NoError(t, s.Observe(Beat{Peer: "A", Incarnation: 1, Accepts: true}))
	require.Equal(t, []string{"A"}, ids(s.Live()))
	require.Empty(t, s.Stable())

	s.Evict("never-seen") // a no-op, not a panic
}

func TestAnAbsentPeerIsForgottenAfterForget(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	s, err := New(Options{Liveness: 5 * time.Second, Stabilize: 60 * time.Second, Forget: 120 * time.Second, Now: c.now})
	require.NoError(t, err)

	require.NoError(t, s.Observe(Beat{Peer: "A", Incarnation: 1, Accepts: true}))
	c.advance(100 * time.Second)
	_, ok := s.Get("A")
	require.True(t, ok)

	c.advance(30 * time.Second)
	_, ok = s.Get("A")
	require.False(t, ok, "an absent peer is eventually dropped so the view is bounded")
	require.Empty(t, s.All())
}

func TestAllListsEveryRememberedPeerOrdered(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	s := newSet(t, c)
	for _, p := range []string{"C", "A", "B"} {
		require.NoError(t, s.Observe(Beat{Peer: p, Incarnation: 1, Accepts: true}))
	}
	c.advance(10 * time.Second)
	all := s.All()
	require.Equal(t, []string{"A", "B", "C"}, ids(all))
	for _, m := range all {
		require.False(t, m.Live)
	}

	// Ordering is by peer id and does not depend on map iteration order.
	for i := 0; i < 20; i++ {
		require.Equal(t, all, s.All())
	}
}

func TestPlacementViewsCarryTheDeclineThrough(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	s := newSet(t, c)
	for i := 0; i <= 60; i++ {
		require.NoError(t, s.Observe(Beat{Peer: "A", Incarnation: 1, Accepts: true}))
		require.NoError(t, s.Observe(Beat{Peer: "B", Incarnation: 1, Accepts: i < 30}))
		c.advance(time.Second)
	}

	require.Equal(t, []placement.Member{
		{ID: "A", Accepts: true},
		{ID: "B", Accepts: false},
	}, s.LivePlacement())
	require.Equal(t, []placement.Member{
		{ID: "A", Accepts: true},
		{ID: "B", Accepts: false},
	}, s.StablePlacement())

	// And the decline is what placement sees: B is in the member list, and is
	// never desired.
	require.Equal(t, []string{"A"}, placement.Desired("index/s001", s.StablePlacement(), 2))
}

func TestConcurrentObserveAndReadDoNotRace(t *testing.T) {
	// -race turns this into the assertion; without it, it is still a check
	// that nothing deadlocks between advance() and the doors that call it.
	c := &clock{t: time.Unix(1000, 0)}
	s, err := New(Options{Liveness: 5 * time.Second, Stabilize: 60 * time.Second, Now: time.Now})
	require.NoError(t, err)
	_ = c

	done := make(chan struct{})
	for w := 0; w < 4; w++ {
		go func(w int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 200; i++ {
				require.NoError(t, s.Observe(Beat{Peer: string(rune('A' + w)), Incarnation: uint64(i), Accepts: i%2 == 0}))
				s.Live()
				s.Stable()
				s.All()
				s.Get("A")
				s.StablePlacement()
			}
		}(w)
	}
	for w := 0; w < 4; w++ {
		<-done
	}
	require.Len(t, s.Live(), 4)
}

// TestABeatCarriesNoClockOfItsOwn is the census behind the package doc's two
// structural claims: a peer cannot say WHEN, so it cannot extend its own
// expiry; and it cannot say HOW FULL, so there is nothing for placement to
// score that a node could lie about. Adding a field to Beat fails here, which
// is where the argument for adding it has to be written down.
func TestABeatCarriesNoClockOfItsOwn(t *testing.T) {
	tp := reflect.TypeOf(Beat{})
	var fields []string
	for i := 0; i < tp.NumField(); i++ {
		fields = append(fields, tp.Field(i).Name)
	}
	sort.Strings(fields)
	require.Equal(t, []string{"Accepts", "Incarnation", "Peer"}, fields)

	for i := 0; i < tp.NumField(); i++ {
		require.NotEqual(t, reflect.TypeOf(time.Time{}), tp.Field(i).Type)
		require.NotEqual(t, reflect.TypeOf(time.Duration(0)), tp.Field(i).Type)
	}
}
