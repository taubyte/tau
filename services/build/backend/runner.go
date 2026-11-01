package backend

import (
	"context"
	"errors"
	"fmt"
	"time"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/queue"
)

// step performs one cycle of the claim loop, which Start calls on a ticker; tests
// call it synchronously with an injected clock. It reports whether it did
// anything.
func (s *Service) step(ctx context.Context) (bool, error) {
	// A FOLLOWER CONSUMES NOTHING, and this is not papered over: the queue
	// would refuse every write anyway, and scanning for an item that could not
	// be claimed is work done to reach a guaranteed error.
	if !s.store.IsLeader() {
		return false, queue.ErrNotLeader
	}

	// The dead letter is drained first, or an exhausted job would read Open and count
	// against its repository's limit forever.
	drained, err := s.drainDeadLetter()
	if err != nil {
		return drained, err
	}

	// Sweep so that an exhausted item reaches the dead letter even when nobody
	// claims. Claim runs the same pass first, but a queue whose items are all
	// claimed and expiring has nothing to claim, and that is exactly when the
	// pass is needed.
	if err := s.work.Sweep(); err != nil {
		return drained, fmt.Errorf("sweep: %w", err)
	}

	if s.runner == nil {
		return drained, nil
	}

	d, err := s.work.Claim()
	if errors.Is(err, queue.ErrNoItem) {
		return drained, nil
	}
	if err != nil {
		return drained, fmt.Errorf("claim: %w", err)
	}

	return true, s.run(ctx, d)
}

// run executes one claimed delivery.
func (s *Service) run(ctx context.Context, d queue.Delivery) error {
	deadline := s.claimDeadline(d)

	// The run's context exists before the record is claimed, since begin registers its
	// cancel inside the claiming section. The bound is a duration off this service's
	// clock, because WithDeadline compares against the wall clock.
	runCtx, stop := context.WithTimeout(ctx, deadline.Sub(s.now()))
	defer stop()

	// Both acks are inside the sections that decided them (begin's drop, settle's);
	// the build in between holds no lock.
	rec, claimed, err := s.begin(d, deadline, stop)
	if err != nil || !claimed {
		return err
	}
	defer s.forget(d.ID)

	work := Work{
		JobID:         rec.ID,
		RepositoryKey: rec.RepositoryKey,
		Ref:           rec.Ref,
		CommitSHA:     rec.CommitSHA,
		Before:        s.lastCommit(rec.RepositoryKey, rec.Ref),
		ProjectID:     rec.ProjectID,
		Attempt:       rec.Attempt,
	}

	// The build minute starts after the claim, before the run.
	started := s.now()

	out, runErr := s.invoke(runCtx, work)
	if out == nil {
		// The run never started (no log could be opened): a failed build, which hands the
		// job to another node.
		s.accountBuild(ctx, rec, started, runErr)
		return s.settle(ctx, d, nil, runErr)
	}
	// Whatever happens, the log closes and its scratch file goes.
	defer out.discard()

	// A shutdown abandons the delivery rather than failing the job: left unacked, the
	// claim expires and the queue redelivers. The cause is read, not the error, so a
	// failure at shutdown also costs only a redelivery. A run that returned nil is
	// settled.
	if runErr != nil && errors.Is(context.Cause(ctx), errShuttingDown) {
		return fmt.Errorf("job %s abandoned: %w", rec.ID, errShuttingDown)
	}

	// Nothing is published for an abandoned delivery, which writes no record. settle
	// publishes after its ack.
	s.accountBuild(ctx, rec, started, runErr)
	return s.settle(ctx, d, out, runErr)
}

// accountBuild emits what one run occupied a builder for, before settle, so a slow
// meter cannot stall a section. Failed builds are accounted too.
func (s *Service) accountBuild(ctx context.Context, rec jobRecord, started time.Time, runErr error) {
	s.emit(ctx, Accounted{
		Project:    rec.ProjectID,
		Repository: rec.RepositoryKey,
		Job:        rec.ID,
		Attempt:    rec.Attempt,
		Op:         OpBuild,
		Duration:   s.now().Sub(started),
		Succeeded:  runErr == nil,
	})
}

// begin is the claim's transition, one section: the barrier, the re-read, then
// either the entry writing the record Claimed plus the registration that lets
// Cancel stop the run, or the ack that drops the delivery. The drop's ack is here
// because an ack outside the section could destroy a concurrent reopening's
// deduplicated item. Re-reading alone is not enough: without the section, a
// cancel between read and entry would be overwritten with Claimed. Not claimed is
// not an error.
func (s *Service) begin(d queue.Delivery, deadline time.Time, stop context.CancelFunc) (jobRecord, bool, error) {
	var claimed jobRecord
	var running bool

	err := s.withJob(d.ID, func(j *held) error {
		// Cancelled is one of the three terminal states, so this is the cancel
		// case as much as it is the redelivery case. An item with no record
		// cannot be identified, let alone built.
		if !j.found() || j.rec().terminal() {
			return j.ack(s.work, d)
		}

		rec := j.rec()
		rec.Status = int32(buildv2.JobStatus_JobStatusClaimed)
		rec.Attempt++
		rec.UpdatedAt = s.now().Unix()
		rec.ClaimedUntil = deadline.UnixNano()
		rec.Holder = s.host.ID().String()

		// The entry's key is the section's subject, not the record's id.
		if err := j.writes().record(rec).commit("claiming"); err != nil {
			return err
		}

		// The registration is the body's last act; a refused one throws the claim away.
		claimed, running = rec, true
		return j.register(stop)
	})
	if err != nil {
		return jobRecord{}, false, err
	}
	return claimed, running, nil
}

// forget drops this node's cancel for a finished run; it needs no reading since the
// entry it drops is always this goroutine's.
func (s *Service) forget(id string) {
	s.runningMu.Lock()
	delete(s.running, id)
	s.runningMu.Unlock()
}

// invoke opens the run's output and runs the job, returning the output even when
// the run failed: those are the lines somebody wants.
func (s *Service) invoke(ctx context.Context, work Work) (out *runOutput, err error) {
	if out, err = s.output(work); err != nil {
		return nil, err
	}

	// A panicking runner is a failed build, not a dead loop or a lingering claim. Why a
	// run failed is the last line of its log, since the record carries no reason.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("runner panicked: %v", r)
		}
		if err != nil {
			fmt.Fprintf(out, "error: %v\n", err)
		}
	}()

	return out, s.runner.Run(ctx, work, out)
}

// settle acks, hands off the log, then writes the record: two sections with the
// network call between them.
//
// The ack is the commit point: a node whose claim expired or which lost leadership
// learns it in the first section and publishes nothing. The barrier is withJob's
// like every section's, and the ack an ordinary write after the read. The first
// section also asks whether the output is still wanted (record gone, or cancelled
// and retried mid-build).
//
// The publish is outside both sections because jobLock covers the whole job table
// and a slow asset store would stall every claim, Enqueue and Cancel on the node.
//
// The second section reads again, since the record may have become terminal (keep
// the status, record the log), stayed Claimed by this run, or been reopened by a
// cancel and retry (put the item back). Publishing before the record lets an
// unreadable log fail the build. The cost: one more barrier per build, and a
// window as wide as the publish in which a leadership loss leaves an unnamed
// object (doc.go).
func (s *Service) settle(ctx context.Context, d queue.Delivery, out *runOutput, runErr error) error {
	var wanted bool
	err := s.withJob(d.ID, func(j *held) error {
		if err := j.ack(s.work, d); err != nil {
			return err
		}
		if !j.found() {
			return nil
		}
		// A record that is neither terminal nor this run's claim is the
		// reopening above: its item goes back and this run's output is nobody's.
		if !j.rec().terminal() && j.rec().status() != buildv2.JobStatus_JobStatusClaimed {
			return j.requeue()
		}
		wanted = true
		return nil
	})
	if err != nil || !wanted {
		return err
	}

	// OUTSIDE EVERY SECTION, holding nothing. Past the ack, so this delivery was
	// still this node's when it was given up; past the read, so there is a
	// record for the cid to reach.
	p, logErr := out.publish(ctx)
	if runErr == nil {
		// A log that could not be read back fails the build; a run that already failed
		// keeps its own reason.
		runErr = logErr
	}

	return s.withJob(d.ID, func(j *held) error {
		if !j.found() {
			return nil
		}
		if j.rec().terminal() {
			// A cancelled job keeps its status and still gets its log; a log that could not be
			// read back is simply dropped here.
			return j.logged(p)
		}
		if j.rec().status() != buildv2.JobStatus_JobStatusClaimed {
			// The reopened job keeps this run's log, filed under the run's attempt; its status
			// is untouched. Record first, push second.
			if err := j.logged(p); err != nil {
				return err
			}
			return j.requeue()
		}

		if runErr != nil {
			// AN ERROR FROM Run IS TERMINAL. The queue's retry exists for a
			// worker that DIED — which is a claim that expired, not an error
			// returned — and retrying a compile error five times is waste.
			return j.fail(runErr.Error(), p)
		}

		// The ref anchor moves in the same entry as the success: this commit is what the
		// next build of this ref diffs from.
		return j.succeed(p)
	})
}

// claimDeadline is when this run must be over: the claim's deadline less the
// margin (a run reaching it is a second claimer), or BuildTimeout if smaller.
func (s *Service) claimDeadline(d queue.Delivery) time.Time {
	lease := d.Deadline.Add(-s.cfg.ClaimMargin)
	policy := s.now().Add(s.cfg.BuildTimeout)
	if policy.Before(lease) {
		return policy
	}
	return lease
}

// lastCommit is the "before" of the next diff: the commit this service itself
// last FINISHED for this exact (repository, ref). Absent means there is nothing
// to diff against and the Runner must treat it as a full build — exactly what a
// GitHub payload's all-zero `before` signals for a newly created branch.
func (s *Service) lastCommit(repositoryKey, ref string) string {
	raw, exists := s.store.Get(refLastKey(repositoryKey, ref))
	if !exists {
		return ""
	}
	return string(raw)
}

// drainDeadLetter marks one exhausted job Failed and acks it. The claim is outside
// the section; the read, record and ack inside, so a cancel cannot be overwritten
// with Failed.
func (s *Service) drainDeadLetter() (bool, error) {
	d, err := s.dead.Claim()
	if errors.Is(err, queue.ErrNoItem) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("dead letter claim: %w", err)
	}

	return true, s.withJob(d.ID, func(j *held) error {
		if j.found() && !j.rec().terminal() {
			if err := j.finish(buildv2.JobStatus_JobStatusFailed, "attempts exhausted"); err != nil {
				return err
			}
		}
		if err := j.ack(s.dead, d); err != nil {
			return fmt.Errorf("dead letter %w", err)
		}
		return nil
	})
}
