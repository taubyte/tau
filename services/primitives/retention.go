package primitives

import (
	"context"
	"errors"
	"time"

	"github.com/taubyte/tau/pkg/queue"
	"github.com/taubyte/tau/pkg/raft"
)

// retention.go is the lifecycle: the pass that ends what a primitive no longer
// keeps, and moves along what nobody is claiming.
//
// # It exists because both halves were lazy, and laziness has a hole
//
// A LOG'S RETENTION RAN ON THE APPENDER. node/log.go swept from Append and from
// nowhere else, which is the one node that may write and the one moment the log
// is known to have changed — and therefore never for a log that went quiet. An
// author who wrote `age: 24h` on a log that stopped receiving got forever.
//
// A QUEUE'S ENFORCEMENT RUNS ON THE CLAIMER. pkg/queue moves expired claims and
// buries exhausted items at the top of Claim, deliberately and for a good
// reason — a sweeper is a second lifecycle to get wrong, and enforcement on the
// path that can violate the invariant cannot silently stop. What it cannot do
// is reach a queue nobody is claiming from, which is where an exhausted item
// waits to be buried.
//
// AND THE DEAD LETTER HAD NOTHING AT ALL. A buried item is in a keyspace derived
// from its queue's name, which is not a declared resource, so no consumer can be
// declared for it and none ever will be — and it sits inline in a state machine
// every voter holds in memory. See DefaultDeadLetterRetention.
//
// # It is leader-only, and that is the whole of its coordination
//
// Everything below writes, and a write commits only on the leader. So the pass
// runs everywhere and does nothing anywhere but on the one node that may — no
// election of its own, no lease, nothing to watch. A node that loses leadership
// mid-pass gets a refusal from the store and stops, and the node that won runs
// the same pass on the next tick from the same replicated state.

// run is this service's ONE turn, and everything periodic it does hangs off it.
//
// ONE PERIOD AND NOT TWO. The clock and the lifecycle used to be two numbers —
// a ten-second tick that did per-schedule work and a thirty-second sweep riding
// every third turn of it — because the clock's cost was paid per turn. It is
// not any more: the leader's whole turn is a comparison of two minutes and, at
// most once a minute, one tasking. So a turn that has no minute to hand out
// costs a leader check and returns, and the one number left is [WithSweep].
//
// WHAT IT BOUNDS IS THE LATENESS OF A FIRE, and it is the only thing that does:
// a cron expression's finest grain is a minute, so a period shorter than that
// decides how promptly the minute is NOTICED rather than what the clock can
// express.
func (s *Service) run(ctx context.Context) {
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
func (s *Service) cycle(ctx context.Context) {
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
	// THE TASKING READS NOTHING REPLICATED, so it goes before the barrier. The
	// leader's whole turn is a comparison of two minutes and one message; what
	// decides on replicated state is the pass the TASKED node runs, and that
	// pass takes its own barrier where it runs — see [crons.accept], which is
	// reached over a stream and so cannot inherit anybody's read.
	if s.schedules != nil {
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
func (s *Service) retain(ctx context.Context, cluster raft.Cluster) {
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

// unresolved says why a place the pass walked was not retained.
//
// A LIFECYCLE THAT STOPPED IS INDISTINGUISHABLE FROM ONE WITH NOTHING TO DO,
// and that is the whole reason this is here rather than a bare return. A
// register this node cannot read stops retention on every resource it holds,
// silently, for as long as it lasts. The one thing this is NOT is an operator's
// step: nothing waits on somebody reading it, the next tick tries again by
// itself. It is evidence, which a silence cannot be.
//
// A RESOURCE NOBODY DECLARES ANY MORE IS NOT THAT. It is a redeploy that
// removed one, its state is reclaim's to end, and saying so every thirty
// seconds would bury the case above in the case that is working as intended.
func unresolved(spec Spec, err error) {
	if errors.Is(err, ErrNotDeclared) {
		return
	}
	logger.Warnf("primitives: %s was not retained: %s", spec.Prefix(), err)
}

// retainLog drops what one log's retention no longer keeps.
func (s *Service) retainLog(ctx context.Context, spec Spec) {
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
func (s *Service) retainQueue(ctx context.Context, spec Spec) {
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
