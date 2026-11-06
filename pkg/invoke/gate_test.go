package invoke

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAGateNeverLetsMoreInThanItHasTokens(t *testing.T) {
	const tokens = 3
	g := newGate(tokens)
	var inside, most atomic.Int64
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				if err := g.enter(t.Context()); err != nil {
					t.Error(err)
					return
				}
				n := inside.Add(1)
				for {
					seen := most.Load()
					if n <= seen || most.CompareAndSwap(seen, n) {
						break
					}
				}
				inside.Add(-1)
				g.leave()
			}
		}()
	}
	wg.Wait()
	require.LessOrEqual(t, most.Load(), int64(tokens))
	require.Equal(t, int64(tokens), g.free.Load(), "every token came back")
}

// queued waits until n callers are queued at g.
func queued(t *testing.T, g *gate, n int) {
	t.Helper()
	require.Eventually(t, func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return len(g.queue) == n
	}, 3*time.Second, time.Millisecond)
}

func TestAGateServesItsQueueInTurn(t *testing.T) {
	g := newGate(1)
	require.NoError(t, g.enter(t.Context()))

	order := make(chan int, 3)
	for i := range 3 {
		go func() {
			if err := g.enter(t.Context()); err != nil {
				t.Error(err)
				return
			}
			order <- i
		}()
		queued(t, g, i+1)
	}

	require.Error(t, g.enter(canceled(t)), "a newcomer does not take the token ahead of the queue")
	for want := range 3 {
		g.leave()
		require.Equal(t, want, <-order)
	}
	g.leave()
	require.Equal(t, int64(1), g.free.Load())
}

func canceled(t *testing.T) context.Context {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	return ctx
}

func TestACallerThatGivesUpWaitingLosesNoToken(t *testing.T) {
	g := newGate(1)
	require.NoError(t, g.enter(t.Context()))

	ctx, cancel := context.WithCancel(t.Context())
	gaveUp := make(chan error, 1)
	go func() { gaveUp <- g.enter(ctx) }()
	queued(t, g, 1)
	cancel()
	require.ErrorIs(t, <-gaveUp, context.Canceled)
	queued(t, g, 0)

	g.leave()
	require.NoError(t, g.enter(t.Context()), "the token the leaver freed is still there")
	g.leave()
}

func TestAGivingUpThatRacesItsTurnGivesTheTokenBack(t *testing.T) {
	g := newGate(2)
	var wg sync.WaitGroup
	for i := range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 100 {
				ctx, cancel := context.WithCancel(t.Context())
				if (i+j)%3 == 0 {
					cancel()
				}
				if g.enter(ctx) == nil {
					g.leave()
				}
				cancel()
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int64(2), g.free.Load(), "no token was kept by a caller that gave up")
	require.Empty(t, g.queue)
}

func TestAnUncontendedGateAllocatesNothing(t *testing.T) {
	g := newGate(1)
	ctx := t.Context()
	require.Zero(t, testing.AllocsPerRun(100, func() {
		if g.enter(ctx) == nil {
			g.leave()
		}
	}))
}
