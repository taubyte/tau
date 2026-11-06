package stats

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// clock is an injected local clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Unix(1_700_000_000, 0)} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func view(t *testing.T, c *clock, opts Options) *View {
	t.Helper()
	opts.Now = c.now
	v, err := New(opts)
	require.NoError(t, err)
	return v
}

func TestAReportCarriesNoClockOfItsOwn(t *testing.T) {
	c := newClock()
	v := view(t, c, Options{Fresh: 10 * time.Second})

	require.NoError(t, v.Observe(Report{Peer: "a", Load: 0.1, Ready: true}))

	n, ok := v.Get("a")
	require.True(t, ok)
	require.Zero(t, n.Age, "age is measured from local receipt, and nothing has happened yet")

	c.advance(30 * time.Second)
	n, ok = v.Get("a")
	require.True(t, ok)
	require.Equal(t, 30*time.Second, n.Age)

	// And the peer cannot undo that by reporting again with the same content:
	// only a new arrival resets age, which is the point.
	require.Empty(t, v.Fresh(), "a 30s-old row is not fresh against a 10s window")
	require.NoError(t, v.Observe(Report{Peer: "a", Load: 0.1, Ready: true}))
	require.Len(t, v.Fresh(), 1)
}

func TestRouteMakesTheCallerSayWhatStaleMeans(t *testing.T) {
	c := newClock()
	v := view(t, c, Options{Fresh: time.Second})

	require.NoError(t, v.Observe(Report{Peer: "a", Load: 0.2, Ready: true, Serving: []string{"shop"}}))
	c.advance(30 * time.Second)

	require.Empty(t, v.Route("shop", 10*time.Second), "a 30s-old row is too old for a caller that said 10s")
	require.Len(t, v.Route("shop", time.Minute), 1, "and current for one that said a minute")
}

func TestRouteAnswersLeastLoadedFirst(t *testing.T) {
	c := newClock()
	v := view(t, c, Options{})

	require.NoError(t, v.Observe(Report{Peer: "busy", Load: 0.9, Ready: true, Serving: []string{"shop"}}))
	require.NoError(t, v.Observe(Report{Peer: "idle", Load: 0.1, Ready: true, Serving: []string{"shop"}}))
	require.NoError(t, v.Observe(Report{Peer: "mid", Load: 0.5, Ready: true, Serving: []string{"shop"}}))

	got := v.Route("shop", time.Minute)
	require.Equal(t, []string{"idle", "mid", "busy"}, peers(got))
}

func TestAnOverstatedLoadOnlyCostsTheLiar(t *testing.T) {
	c := newClock()
	v := view(t, c, Options{})

	require.NoError(t, v.Observe(Report{Peer: "honest", Load: 0.5, Ready: true, Serving: []string{"shop"}}))
	require.NoError(t, v.Observe(Report{Peer: "liar", Load: 1, Ready: true, Serving: []string{"shop"}}))

	got := v.Route("shop", time.Minute)
	require.Equal(t, []string{"honest", "liar"}, peers(got),
		"the exaggerating node sorts last and is still available, which is the whole penalty")
}

func TestTheOnlyServerIsReturnedEvenAtFullLoad(t *testing.T) {
	c := newClock()
	v := view(t, c, Options{})

	require.NoError(t, v.Observe(Report{Peer: "a", Load: 1, Ready: true, Serving: []string{"shop"}}))

	got := v.Route("shop", time.Minute)
	require.Len(t, got, 1)
	require.Equal(t, float64(1), got[0].Load, "the caller can see it is saturated and decide")
}

func TestNotReadyIsNotBusy(t *testing.T) {
	c := newClock()
	v := view(t, c, Options{})

	require.NoError(t, v.Observe(Report{Peer: "draining", Load: 0, Ready: false, Serving: []string{"shop"}}))
	require.Empty(t, v.Route("shop", time.Minute), "load zero does not make a draining node a target")

	require.Len(t, v.All(), 1, "but it is still remembered and still visible")
}

func TestRoutingIsScopedToWhatANodeSaysItServes(t *testing.T) {
	c := newClock()
	v := view(t, c, Options{})

	require.NoError(t, v.Observe(Report{Peer: "a", Ready: true, Serving: []string{"blog", "shop"}}))
	require.NoError(t, v.Observe(Report{Peer: "b", Ready: true, Serving: []string{"blog"}}))

	require.Equal(t, []string{"a"}, peers(v.Route("shop", time.Minute)))
	require.Equal(t, []string{"a", "b"}, peers(v.Route("blog", time.Minute)))
	require.Empty(t, v.Route("absent", time.Minute))

	// A node that stops serving something drops out by reporting a shorter
	// list, so nothing has to notice a removal.
	require.NoError(t, v.Observe(Report{Peer: "a", Ready: true, Serving: []string{"blog"}}))
	require.Empty(t, v.Route("shop", time.Minute))
}

func TestAnOlderIncarnationIsRefused(t *testing.T) {
	c := newClock()
	v := view(t, c, Options{})

	require.NoError(t, v.Observe(Report{Peer: "a", Incarnation: 7, Load: 0.2, Ready: true}))

	err := v.Observe(Report{Peer: "a", Incarnation: 6, Load: 0.9, Ready: true})
	require.ErrorIs(t, err, ErrStaleRun)

	n, _ := v.Get("a")
	require.Equal(t, uint64(7), n.Incarnation)
	require.Equal(t, 0.2, n.Load, "the stale report changed nothing")

	// The same incarnation is an ordinary update — a node reports repeatedly
	// within one run.
	require.NoError(t, v.Observe(Report{Peer: "a", Incarnation: 7, Load: 0.4, Ready: true}))
	n, _ = v.Get("a")
	require.Equal(t, 0.4, n.Load)
}

func TestAReportIsAuthoritativeForItsOwnPeerOnly(t *testing.T) {
	c := newClock()
	v := view(t, c, Options{})

	require.NoError(t, v.Observe(Report{Peer: "a", Load: 0.1, Ready: true}))
	require.NoError(t, v.Observe(Report{Peer: "b", Load: 0.9, Ready: true}))

	require.NoError(t, v.Observe(Report{Peer: "b", Load: 0.2, Ready: true}))

	a, _ := v.Get("a")
	require.Equal(t, 0.1, a.Load, "b's report cannot move a's row")
}

func TestASilentPeerIsForgotten(t *testing.T) {
	c := newClock()
	v := view(t, c, Options{Fresh: time.Second, Forget: time.Minute})

	require.NoError(t, v.Observe(Report{Peer: "a", Ready: true}))
	c.advance(30 * time.Second)
	require.Len(t, v.All(), 1, "still remembered while something might ask about it")

	c.advance(31 * time.Second)
	require.Empty(t, v.All())
	_, ok := v.Get("a")
	require.False(t, ok)
}

func TestEvictDropsAPeerImmediately(t *testing.T) {
	c := newClock()
	v := view(t, c, Options{})

	require.NoError(t, v.Observe(Report{Peer: "a", Ready: true, Serving: []string{"shop"}}))
	require.Len(t, v.Route("shop", time.Minute), 1)

	v.Evict("a")
	require.Empty(t, v.Route("shop", time.Minute))
	require.Empty(t, v.All())
	v.Evict("nobody") // evicting an unknown peer is not an error
}

func TestAReportWithNoPeerBelongsToNobody(t *testing.T) {
	c := newClock()
	v := view(t, c, Options{})
	require.ErrorIs(t, v.Observe(Report{Load: 0.5}), ErrNoPeer)
}

func TestLoadIsAFraction(t *testing.T) {
	c := newClock()
	v := view(t, c, Options{})
	require.ErrorIs(t, v.Observe(Report{Peer: "a", Load: 1.5}), ErrLoadRange)
	require.ErrorIs(t, v.Observe(Report{Peer: "a", Load: -0.1}), ErrLoadRange)
	require.NoError(t, v.Observe(Report{Peer: "a", Load: 0}))
	require.NoError(t, v.Observe(Report{Peer: "a", Load: 1}))
}

func TestOptionsRefuseAForgetWindowInsideTheFreshOne(t *testing.T) {
	_, err := New(Options{Fresh: time.Minute, Forget: time.Second})
	require.ErrorIs(t, err, ErrForgetShort)

	_, err = New(Options{Fresh: -time.Second})
	require.Error(t, err)

	v, err := New(Options{})
	require.NoError(t, err)
	require.NotNil(t, v, "defaults are a working view, and the clock defaults to the real one")
}

func TestTheViewDoesNotAliasACallersSlice(t *testing.T) {
	c := newClock()
	v := view(t, c, Options{})

	serving := []string{"shop"}
	require.NoError(t, v.Observe(Report{Peer: "a", Ready: true, Serving: serving}))
	serving[0] = "somebody-elses-site"

	require.Equal(t, []string{"a"}, peers(v.Route("shop", time.Minute)))

	// And the row handed out is a copy too.
	got := v.Route("shop", time.Minute)
	got[0].Serving[0] = "mutated"
	require.Equal(t, []string{"a"}, peers(v.Route("shop", time.Minute)))
}

func TestConcurrentUse(t *testing.T) {
	c := newClock()
	v := view(t, c, Options{})

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for range 50 {
				peer := string(rune('a' + i))
				if err := v.Observe(Report{Peer: peer, Load: 0.5, Ready: true, Serving: []string{"shop"}}); err != nil {
					t.Error(err)
					return
				}
				v.Route("shop", time.Minute)
				v.All()
				v.Fresh()
			}
		}(i)
	}
	wg.Wait()
	require.Len(t, v.All(), 8)
}

func TestErrorsAreDistinguishable(t *testing.T) {
	require.False(t, errors.Is(ErrNoPeer, ErrStaleRun))
}

func peers(ns []Node) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, n.Peer)
	}
	return out
}

func TestAddressesAreCarriedAndCopied(t *testing.T) {
	c := newClock()
	v := view(t, c, Options{})

	addrs := []string{"203.0.113.7", "2001:db8::1", "edge.example.com"}
	require.NoError(t, v.Observe(Report{
		Peer: "a", Ready: true, Serving: []string{"shop"}, Addresses: addrs,
	}))

	got := v.Route("shop", time.Minute)
	require.Len(t, got, 1)
	require.Equal(t, addrs, got[0].Addresses,
		"a hostname is as valid here as an A record: parsing is the reader's question")

	// The caller may reuse its slice, and the row handed out is a copy too.
	addrs[0] = "somebody-elses-box"
	got[0].Addresses[1] = "mutated"
	require.Equal(t,
		[]string{"203.0.113.7", "2001:db8::1", "edge.example.com"},
		v.Route("shop", time.Minute)[0].Addresses)
}

func TestWithinKeepsFreshNodes(t *testing.T) {
	now := time.Unix(0, 0)
	v, err := New(Options{Now: func() time.Time { return now }, Fresh: time.Minute})
	require.NoError(t, err)

	for _, peer := range []string{"n2", "n1", "n3"} {
		require.NoError(t, v.Observe(Report{Peer: peer, Load: 0.1, Ready: true}))
	}

	var peers []string
	for _, n := range v.Within(time.Minute) {
		peers = append(peers, n.Peer)
	}
	require.Equal(t, []string{"n1", "n2", "n3"}, peers)

	now = now.Add(2 * time.Minute)
	require.Empty(t, v.Within(time.Minute), "a stale node was still placeable")
}

func TestTheIndexFollowsWhatANodeServes(t *testing.T) {
	c := newClock()
	v := view(t, c, Options{})
	require.NoError(t, v.Observe(Report{Peer: "a", Ready: true, Serving: []string{"shop", "blog"}}))
	require.NoError(t, v.Observe(Report{Peer: "b", Ready: true, Serving: []string{"shop"}}))
	require.Equal(t, []string{"a", "b"}, peers(v.Route("shop", time.Minute)))

	require.NoError(t, v.Observe(Report{Peer: "a", Ready: true, Serving: []string{"blog"}}))
	require.Equal(t, []string{"b"}, peers(v.Route("shop", time.Minute)), "a stopped serving shop")
	require.Equal(t, []string{"a"}, peers(v.Route("blog", time.Minute)))

	require.NoError(t, v.Observe(Report{Peer: "a", Ready: true, Serving: []string{"blog"}}), "the same list again")
	require.Equal(t, []string{"a"}, peers(v.Route("blog", time.Minute)))

	v.Evict("a")
	require.Empty(t, v.Route("blog", time.Minute))
	v.mu.RLock()
	_, kept := v.names["blog"]
	v.mu.RUnlock()
	require.False(t, kept, "a name nobody serves leaves the index")
}

func TestAForgottenRowIsNoAnswerAndIsDroppedOnALaterWrite(t *testing.T) {
	c := newClock()
	v := view(t, c, Options{Fresh: time.Second, Forget: time.Minute})
	require.NoError(t, v.Observe(Report{Peer: "gone", Ready: true, Serving: []string{"shop"}}))
	c.advance(2 * time.Minute)

	require.Empty(t, v.Route("shop", time.Hour), "older than Forget, whatever the caller tolerates")
	require.Empty(t, v.RoutePeers("shop", time.Hour, nil))
	require.Empty(t, v.All())
	_, ok := v.Get("gone")
	require.False(t, ok)

	require.NoError(t, v.Observe(Report{Peer: "here", Ready: true, Serving: []string{"blog"}}))
	v.mu.RLock()
	defer v.mu.RUnlock()
	require.NotContains(t, v.rows, "gone")
	require.NotContains(t, v.names, "shop")
}

func TestRoutePeersIsRoutesOrderAndCopiesNothing(t *testing.T) {
	c := newClock()
	v := view(t, c, Options{})
	for i, load := range []float64{0.5, 0.1, 0.5, 0.9, 0.1} {
		require.NoError(t, v.Observe(Report{Peer: fmt.Sprintf("p%d", i), Load: load, Ready: true, Serving: []string{"shop"}}))
	}
	require.NoError(t, v.Observe(Report{Peer: "busy", Load: 0.2, Ready: false, Serving: []string{"shop"}}))

	want := peers(v.Route("shop", time.Minute))
	require.Equal(t, []string{"p1", "p4", "p0", "p2", "p3"}, want, "least loaded first, a tie by peer, not-ready left out")
	require.Equal(t, want, v.RoutePeers("shop", time.Minute, nil))

	dst := make([]string, 0, 8)
	require.Zero(t, testing.AllocsPerRun(100, func() { v.RoutePeers("shop", time.Minute, dst[:0]) }))
}

func TestServesIsAskedOfTheIndex(t *testing.T) {
	c := newClock()
	v := view(t, c, Options{Fresh: time.Second, Forget: time.Minute})
	require.NoError(t, v.Observe(Report{Peer: "a", Serving: []string{"storage", "worker"}}))
	require.True(t, v.Serves("a", "storage"), "ready or not, fresh or not: a row says what it serves")
	require.False(t, v.Serves("a", "database"))
	require.False(t, v.Serves("nobody", "storage"))

	c.advance(2 * time.Minute)
	require.False(t, v.Serves("a", "storage"), "a forgotten row serves nothing")

	require.Zero(t, testing.AllocsPerRun(100, func() { v.Serves("a", "storage") }))
}
