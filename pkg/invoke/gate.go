package invoke

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
)

// gate is a counting semaphore whose uncontended way in and out is one atomic each.
// The pool passes one on every call; a channel used as one locks on both sides, and
// on eight cores that lock held a group to one core's throughput.
//
// A caller finding no token queues, and a token freed while anyone queues goes to the
// head of the queue: once one caller waits, a newcomer waits behind it rather than
// taking the next token first, so a queued call is not starved under load.
type gate struct {
	free    atomic.Int64
	waiting atomic.Int64

	mu    sync.Mutex
	queue []chan struct{}
}

func newGate(tokens int) *gate {
	g := &gate{}
	g.free.Store(int64(tokens))
	return g
}

func (g *gate) take() bool {
	for {
		n := g.free.Load()
		if n <= 0 {
			return false
		}
		if g.free.CompareAndSwap(n, n-1) {
			return true
		}
	}
}

// enter takes a token, waiting its turn for one until ctx ends.
func (g *gate) enter(ctx context.Context) error {
	if g.waiting.Load() == 0 && g.take() {
		return nil
	}

	// waiting is raised before the token is looked for again, and leave adds its
	// token before reading waiting, so either this sees the token or leave sees
	// this waiter and hands it over: a token is never freed past a sleeping caller.
	g.waiting.Add(1)
	defer g.waiting.Add(-1)

	g.mu.Lock()
	if len(g.queue) == 0 && g.take() {
		g.mu.Unlock()
		return nil
	}
	turn := make(chan struct{}, 1)
	g.queue = append(g.queue, turn)
	g.mu.Unlock()

	select {
	case <-turn:
		return nil
	case <-ctx.Done():
	}

	g.mu.Lock()
	if i := slices.Index(g.queue, turn); i >= 0 {
		g.queue = slices.Delete(g.queue, i, i+1)
		g.mu.Unlock()
		return ctx.Err()
	}
	g.mu.Unlock()

	// Handed a token as ctx ended: it is given back, not lost.
	<-turn
	g.leave()
	return ctx.Err()
}

// leave gives a token back, to the first caller queued for one if any is.
func (g *gate) leave() {
	g.free.Add(1)
	if g.waiting.Load() == 0 {
		return
	}

	g.mu.Lock()
	for len(g.queue) > 0 && g.take() {
		turn := g.queue[0]
		g.queue = g.queue[1:]
		turn <- struct{}{}
	}
	g.mu.Unlock()
}
