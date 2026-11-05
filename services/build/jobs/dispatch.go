package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/pkg/queue"
	"github.com/taubyte/tau/services/build/run"
)

// The runners a build node hands runs to. The node leading the queue claims a job
// for each runner discovery names that holds none of its runs, and hands it over
// the runner's own protocol (pkg/proto/tau/runner/v2): Plan or Build, then Seal once
// the job is settled. A runner that cannot be reached or turns the run down costs
// the run, not the build: it is abandoned, its claim lapses, and the queue hands it
// to another runner.

// errRunnerLost is a run whose runner went away or turned it down. It is not the
// build's failure: the run is abandoned, its claim lapses, and the queue hands the job
// to another runner.
var errRunnerLost = errors.New("build: the runner running it went away")

// Runners is the runners a build node can hand a run to.
type Runners interface {
	// Peers is every runner there is now.
	Peers() []peer.ID
	// At is the runner id.
	At(id peer.ID) (run.Service, error)
}

// busy is the runners holding one of this node's runs.
type busy struct {
	mu   sync.Mutex
	held map[peer.ID]bool
	// wake rings when there is something to hand out or someone to hand it to: a job
	// queued, a runner done with one. The claim loop dispatches on it rather than on
	// its next tick.
	wake notice
}

// take is id holding a run from now, false when it holds one already.
func (b *busy) take(id peer.ID) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.held[id] {
		return false
	}
	if b.held == nil {
		b.held = map[peer.ID]bool{}
	}
	b.held[id] = true
	return true
}

// release is id free again, the loop not told: nothing was handed to it.
func (b *busy) release(id peer.ID) {
	b.mu.Lock()
	delete(b.held, id)
	b.mu.Unlock()
}

// give is id done with a run, and the loop told.
func (b *busy) give(id peer.ID) {
	b.release(id)
	b.wake.ring()
}

// dispatch claims a job for every free runner and runs each on its own: a run per
// runner, every write still a section under jobLock, as an rpc goroutine's is. It
// reports whether it handed anything out.
func (s *server) dispatch(ctx context.Context) (bool, error) {
	if s.runners == nil {
		return false, nil
	}
	var handed bool
	for _, id := range s.runners.Peers() {
		if !s.busy.take(id) {
			continue
		}
		d, err := s.work.Claim()
		if err != nil {
			s.busy.release(id)
			if errors.Is(err, queue.ErrNoItem) {
				return handed, nil
			}
			return handed, fmt.Errorf("claim: %w", err)
		}
		handed = true
		s.dispatched.Add(1)
		go s.runOn(ctx, d, id)
	}
	return handed, nil
}

// runOn is one dispatched job, its runner free again once the job has ended. Its
// error is the loop's kind, with nobody to return it to: the next cycle and the
// claim's lapse are the retry.
func (s *server) runOn(ctx context.Context, d queue.Delivery, id peer.ID) {
	defer s.dispatched.Done()
	defer s.busy.give(id)
	r, err := s.runners.At(id)
	if err != nil {
		return
	}
	s.run(ctx, d, id, r)
}
