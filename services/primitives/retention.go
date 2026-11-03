package primitives

import (
	"context"
	"errors"
	"time"

	"github.com/taubyte/tau/pkg/queue"
	"github.com/taubyte/tau/pkg/raft"
)

// The lifecycle pass: end what a primitive no longer keeps and move along what nobody
// claims. pkg/queue enforces on Claim, which cannot reach an idle queue; a log that
// stopped receiving still needs its age budget; a dead letter has no consumer at all
// (see DefaultDeadLetterRetention). Leader-only: the pass runs everywhere, writes only
// on the leader, and a new leader runs it next tick from the same state.

// run is this service's one periodic turn, every WithSweep. It bounds how late a fire
// is noticed; a cron's grain is a minute.
func (s *server) run(ctx context.Context) {
	t := time.NewTicker(s.sweep)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.cycle(ctx)
		}
	}
}

// cycle is one turn: the leader's tasking, and the lifecycle pass.
func (s *server) cycle(ctx context.Context) {
	cluster, err := s.consensus()
	if err != nil {
		return
	}
	if !cluster.IsLeader() {
		// NOT AN ERROR AND NOT A WAIT. Some other node is running this pass over
		// the same replicated state; this one runs it the turn after it wins an
		// election.
		//
		// AND A FOLLOWER TASKS NOTHING, which is what makes a fleet fire once:
		// raft already elected one node to be the clock, so leadership IS the
		// exclusion and there is nothing else to arrange.
		return
	}
	// The tasking reads nothing replicated, so it goes before the barrier; the tasked
	// node's pass takes its own (crons.accept).
	if _, ok := s.workers(); ok {
		s.crons.tick(ctx, s.clock())
	}

	// RETENTION DECIDES ON WHAT IT READS, so it reads behind a barrier: a node
	// that has just become leader can hold committed entries it has not
	// applied, and a retention decided on stale state drops records another
	// leader's writes already moved past.
	if err := cluster.Barrier(applyTimeout); err != nil {
		return
	}
	s.retain(ctx, cluster)
}

// retain is one lifecycle pass over everything this node's namespace declares.
func (s *server) retain(ctx context.Context, cluster raft.Cluster) {
	for _, spec := range logsIn(cluster) {
		if ctx.Err() != nil {
			return
		}
		s.retainLog(ctx, spec)
	}
	for _, spec := range queuesIn(cluster) {
		if ctx.Err() != nil {
			return
		}
		s.retainQueue(ctx, spec)
	}
}

// unresolved says why a place the pass walked was not retained. A register this node
// cannot read stops retention silently, so it is logged; the next tick retries. A
// resource no longer declared is reclaim's and not logged.
func unresolved(spec Spec, err error) {
	if errors.Is(err, ErrNotDeclared) {
		return
	}
	logger.Warnf("primitives: %s was not retained: %s", spec.Prefix(), err)
}

// retainLog drops what one log's retention no longer keeps.
func (s *server) retainLog(ctx context.Context, spec Spec) {
	d, err := s.declared(ctx, spec.at(), logType)
	if err != nil {
		// A LOG THIS NODE CANNOT RESOLVE IS NOT ONE IT MAY TRUNCATE. A transport
		// error is not a fact about the declaration, and a log whose resource
		// was deleted is reclaim's business — which is gated on a revision
		// rather than on a lookup that failed.
		unresolved(spec, err)
		return
	}
	if !d.keep.bounded() {
		return
	}

	dropped, err := s.records.sweep(ctx, spec, d.keep)
	if err != nil {
		logger.Warnf("primitives: retention on %s: %s", spec.Prefix(), err)
		return
	}
	if dropped > 0 {
		logger.Debugf("primitives: %s dropped %d records", spec.Prefix(), dropped)
	}
}

// retainQueue moves one queue along and bounds its dead letter.
func (s *server) retainQueue(ctx context.Context, spec Spec) {
	d, err := s.declared(ctx, spec.at(), queueType)
	if err != nil {
		unresolved(spec, err)
		return
	}

	held, err := s.work.open(d)
	if err != nil {
		return
	}

	// THE SWEEP FIRST, BECAUSE IT IS WHAT FILLS THE DEAD LETTER. It runs the
	// same pass Claim runs — expired claims back to ready, exhausted ones
	// buried — so the two cannot drift, and an item that gives up on this pass
	// is bounded by the expiry below on the next one rather than waiting a full
	// interval to be seen at all.
	if err := held.Sweep(); err != nil && !errors.Is(err, queue.ErrNotLeader) {
		logger.Warnf("primitives: sweeping %s: %s", spec.Prefix(), err)
	}

	// UNCLAIMED WORK PAST ITS AGE MOVES TO THE DEAD LETTER, before the dead letter
	// is bounded below, so what retires this pass is kept as evidence like
	// anything else buried.
	if retired, err := held.Retire(d.keep.age); err != nil && !errors.Is(err, queue.ErrNotLeader) {
		logger.Warnf("primitives: retiring what %s left unclaimed: %s", spec.Prefix(), err)
	} else if retired > 0 {
		logger.Debugf("primitives: %s moved %d unclaimed items to its dead letter", spec.Prefix(), retired)
	}

	dead, err := held.Dead()
	if err != nil {
		return
	}
	dropped, err := dead.Expire(s.dead.age, s.dead.size)
	if err != nil && !errors.Is(err, queue.ErrNotLeader) {
		logger.Warnf("primitives: expiring the dead letter of %s: %s", spec.Prefix(), err)
		return
	}
	if dropped > 0 {
		logger.Debugf("primitives: %s dropped %d dead-lettered items", spec.Prefix(), dropped)
	}
}
