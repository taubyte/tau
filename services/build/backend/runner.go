package backend

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/taubyte/tau/pkg/raft"
	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
	"github.com/taubyte/tau/pkg/v2/queue"
)

// step performs exactly ONE cycle of the claim loop, and the loop Start spawns
// does nothing but call it on a ticker.
//
// That split is deliberate and is what makes this path testable: a background
// goroutine reached only through a real clock has branches nothing can reach,
// and the branches here are the ones that matter — a claim that expires, a
// runner that panics, a cancel mid-run, a leadership flap between the run and
// the acknowledgement. A test calls step synchronously with an injected clock
// and a fake Runner.
//
// It reports whether it did anything, so a caller that wants to drain rather
// than poll can loop until it did not.
func (s *Service) step(ctx context.Context) (bool, error) {
	// A FOLLOWER CONSUMES NOTHING, and this is not papered over: the queue
	// would refuse every write anyway, and scanning for an item that could not
	// be claimed is work done to reach a guaranteed error.
	if !s.store.IsLeader() {
		return false, queue.ErrNotLeader
	}

	// THE DEAD LETTER IS DRAINED FIRST. Without it a job that exhausts its
	// attempts sits there forever while its record still reads Open, its open
	// index row still counts against the repository's limit, and nothing ever
	// notices. It is another Queue on the same Group, so draining it is an
	// ordinary claim and ack.
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

	// THE RUN'S CONTEXT IS BUILT BEFORE THE RECORD IS CLAIMED, because its
	// cancel is what Cancel stops the run with and begin registers it inside
	// the same section as the claim. Built after, there is a window in which
	// the record reads Claimed and nothing is registered, and a cancel landing
	// in it stops nothing.
	//
	// The bound is applied as a DURATION off this service's own clock, not as
	// an absolute deadline: context.WithDeadline compares against the wall
	// clock, and this service reads whatever clock it was given. In production
	// they are the same clock and the two forms are identical; anywhere they
	// are not — a test that moves time, a node whose clock is being stepped —
	// the absolute form would end a run the instant it started, or never.
	runCtx, stop := context.WithTimeout(ctx, deadline.Sub(s.now()))
	defer stop()

	// BOTH OF THIS FUNCTION'S ACKS ARE INSIDE THE SECTION THAT DECIDED THEM, and
	// neither is here: begin acks the delivery it drops, settle acks the one it
	// judges. What is left in between is the build, which is the one thing that
	// must NOT hold the lock.
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

	runErr := s.invoke(runCtx, work)

	// A SHUTDOWN ABANDONS THE DELIVERY RATHER THAN FAILING THE JOB. Close ends
	// the loop's context so that a restart does not wait out an hour-long build,
	// and the run returns the moment it does — but that run was interrupted, not
	// judged, and acking it here would mark Failed a build nothing finished, on
	// EVERY rolling restart. Left unacknowledged, the item stays claimed, the
	// claim expires, and the queue redelivers it: the crashed-worker path, which
	// this design already has.
	//
	// It is the CAUSE that is read, not the error. A build that failed at the
	// instant of a shutdown is indistinguishable from a run the shutdown cut
	// short, so both are abandoned — which costs one redelivery, where guessing
	// the other way costs the job.
	//
	// A run that RETURNED NOTHING is a run that finished. It is settled as
	// normal: the work is done, the two writes it takes are cheap, and throwing
	// it away would rebuild what this node had already built.
	if runErr != nil && errors.Is(context.Cause(ctx), errShuttingDown) {
		return fmt.Errorf("job %s abandoned: %w", rec.ID, errShuttingDown)
	}

	return s.settle(d, runErr)
}

// begin is the claim's own transition, and it is ONE SECTION: the barrier, the
// re-read that decides whether this delivery is still worth running, and then
// EITHER the entry that writes the record Claimed and the registration that
// lets Cancel stop the run, OR the ack that drops the delivery.
//
// ONE READ, TWO BRANCHES, TWO DIFFERENT WRITES — and that is why the ack is
// here rather than in run. The drop branch's write IS the ack: the read said
// this job has finished, and acking is what that read decided. Made after the
// section closed it was a queue write nothing serialized against a push, and
// pkg/v2/queue's Push deduplicates against the index row of a claimed item
// while Ack deletes that very row, so a reopening that landed in the window
// made a no-op push and this ack destroyed the item it was deduplicated
// against — a job reading Open with nothing queued for it.
//
// RE-READING WAS NEVER ENOUGH ON ITS OWN. It is what makes cancel a record
// write rather than a message, but a read is only true until something else
// writes, and Cancel answers on an rpc goroutine while this runs on the claim
// loop's. Between the read and the entry, a cancel wrote Cancelled, could not
// withdraw an item this node holds, and found nothing to stop — and then this
// entry, decided on the read taken before all of it, put the record back to
// Claimed. The build ran to completion, the job reported Success, and the ref
// anchor moved to the commit the caller had cancelled. raft ordered the two
// entries perfectly; ordering is not the property that was missing.
//
// THE REGISTRATION IS INSIDE, not in invoke below, and that is the other half:
// a cancel serialized AFTER this section must find the run registered, or it
// would write a terminal record while a build it could have stopped ran on to
// its own deadline. It is a method on the handle, like every other write here —
// a map in this process is a write like any other, and "inside the section" was
// the one claim about it that nothing could see: a registration handed to run()
// as a closure built here and invoked after this function returns sits inside
// the span by every measure a scan has.
//
// It reports whether the delivery was claimed. Not claimed is not an error — it
// is a delivery for the caller to ack and drop.
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
		rec.Status = int32(buildv1.JobStatus_JobStatusClaimed)
		rec.Attempt++
		rec.UpdatedAt = s.now().Unix()
		rec.ClaimedUntil = deadline.UnixNano()
		rec.Holder = s.host.ID().String()

		// THE ENTRY'S KEY IS THE SECTION'S, not the record's. It was
		// set(jobRecordKey(rec.ID), value) inside withJob(d.ID, …) until the
		// round that removed the arbitrary key: the two agreed because the
		// record on disk said so, which is a property of the store rather than
		// of the expression, and a record read from anywhere else would have
		// been written wherever it said it belonged.
		if err := j.writes().record(rec).commit("claiming"); err != nil {
			return err
		}

		// THE REGISTRATION IS THE CLAIM'S OTHER HALF, so it is inside this same
		// body and it is the body's last act: a refusal there is a handle whose
		// section had returned, and the two lines below throw the claim away
		// with it — which run() answers by returning before it starts a build
		// nothing could stop.
		claimed, running = rec, true
		return j.register(stop)
	})
	if err != nil {
		return jobRecord{}, false, err
	}
	return claimed, running, nil
}

// forget drops this node's cancel for a run that is over. Its counterpart is
// register(), which begin calls inside the claim's section, and run defers this
// rather than invoke so that the pair is one function's.
//
// IT TAKES NO READING, and section.go says why: it is decided by the run being
// over rather than by a read, it runs outside every section, and the entry it
// drops is always the one this same goroutine registered.
func (s *Service) forget(id string) {
	s.runningMu.Lock()
	delete(s.running, id)
	s.runningMu.Unlock()
}

// invoke runs the job. The context is the claim's, built by run and registered
// by begin, so that a cancel can never land between the record reading Claimed
// and there being something to stop.
func (s *Service) invoke(ctx context.Context, work Work) (err error) {
	// A runner that panics must not take the loop down with it, and must not
	// leave the item claimed until its lease expires either: a panic is a
	// failed build, which is what the recovery turns it into.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("runner panicked: %v", r)
		}
	}()

	return s.runner.Run(ctx, work, &attemptSink{s: s, id: work.JobID, attempt: work.Attempt})
}

// settle ends the delivery and writes what the run decided — THE ACK AND THE
// RECORD WRITE IT DECIDES, ONE SECTION.
//
// ACK IS THE COMMIT POINT, and it comes before anything this run produced is
// published. It is the only compare-and-swap this service has: the queue
// refuses an ack whose delivery it no longer holds, so a node whose claim
// expired — or which lost leadership mid-run — learns it here and PUBLISHES
// NOTHING. Content-addressed rows already written are harmless; a pointer
// written by a node that no longer speaks for the group is not. v1 asked
// whether it still owned the job only when REPORTING, after it had already
// stashed assets and pushed keys to tns.
//
// IT USED TO BE THE SECTION'S BARRIER AS WELL, taken before the read because
// queue.Ack blocks until everything committed is applied. THE TWO JOBS WANT
// OPPOSITE ORDERS. A barrier has to come BEFORE the read it protects — this
// node can regain leadership during a build and hold entries it has not
// applied, so a record read without one can be a Cancel this run cannot see.
// A write has to come AFTER the read that decided it, or it names no read at
// all: an ack taking a bare section took whatever number was running at the
// instant of the write, and a body that split the deciding read from the ack
// across two sections was allowed to destroy the item.
//
// SO THE BARRIER IS withJob'S, LIKE EVERY OTHER SECTION'S, and the ack is an
// ordinary write inside the body. It costs one replicated entry per settled
// build — barrier_test.go spends it — and it buys the rule with no exception in
// it: every write in this package is made by the body that read the record. The
// ack still precedes every write this run publishes, which is the whole of what
// "the commit point" means.
//
// THE RECORD IS RE-READ because the whole build happened outside this lock, and
// three different things can have written it in the meantime. Each is a
// different answer:
//
//   - terminal. A Cancel wrote it while the run was going, and a cancelled job
//     must not be overwritten with the outcome of the run that was cancelled.
//     Its item is the one the ack just removed, which is correct: the job is
//     over.
//   - Claimed, by this run. The ordinary case, and the only one this run's
//     outcome describes.
//   - OPEN. Somebody cancelled the job and then retried it while the build ran,
//     so the record is a NEW attempt that this run knows nothing about — and
//     the retry's push was deduplicated against the very delivery the ack above
//     just destroyed (pkg/v2/queue: Push is a no-op for an id the queue holds,
//     Ack deletes that id's index row). Writing this run's outcome over it
//     tells the caller its retry failed with the error of the build it
//     cancelled; leaving it alone but saying nothing leaves a job that reads
//     Open with no item and, once finish has dropped its open row, nothing for
//     reconcile to walk either. So the item goes back, in this section, where
//     the reopening could not have raced it.
func (s *Service) settle(d queue.Delivery, runErr error) error {
	return s.withJob(d.ID, func(j *held) error {
		if err := j.ack(s.work, d); err != nil {
			return err
		}

		if !j.found() || j.rec().terminal() {
			return nil
		}

		rec := j.rec()
		if rec.status() != buildv1.JobStatus_JobStatusClaimed {
			return j.requeue()
		}

		if runErr != nil {
			// AN ERROR FROM Run IS TERMINAL. The queue's retry exists for a
			// worker that DIED — which is a claim that expired, not an error
			// returned — and retrying a compile error five times is waste.
			return j.finish(buildv1.JobStatus_JobStatusFailed, runErr.Error())
		}

		// The ref anchor is written in the SAME entry as the success, because
		// it is the same fact: this commit is what the next build of this ref
		// diffs from, and it is read from here rather than from a webhook
		// (build.proto: "read from its own job history, never from the
		// frontend"). It used to be handed in as an extra BatchOp this body
		// composed; it is a row of the entry now, built off the record the
		// entry writes, so the anchor a success can move is this job's ref.
		return j.succeed()
	})
}

// claimDeadline is when this run must be over.
//
// THE CLAIM'S OWN DEADLINE IS THE HARD BOUND. A run that reaches it is a second
// claimer of the same job: the queue's repair() puts the item back and this
// very loop's next cycle takes it, so one node builds one job twice. The margin
// is what the ack and the terminal record are written in. BuildTimeout is the
// operator's policy on top, and the smaller of the two wins — so a lease made
// generous does not silently become the build timeout.
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

// drainDeadLetter marks one exhausted job Failed and acknowledges it.
//
// The claim is outside the section; the barrier, the read, the record and the
// ack are inside it. A cancel between the read and the write would be
// overwritten with Failed, and its caller told a build it had stopped failed on
// its own. The claim needs no protection here — the dead letter's own group
// lock is what makes it exclusive — but it is NOT this section's barrier: it is
// another function's, taken before the lock, with an unbounded wait for the
// lock in between.
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
			if err := j.finish(buildv1.JobStatus_JobStatusFailed, "attempts exhausted"); err != nil {
				return err
			}
		}
		if err := j.ack(s.dead, d); err != nil {
			return fmt.Errorf("dead letter %w", err)
		}
		return nil
	})
}

// attemptSink writes what a run produced, one row per entry, under THIS
// attempt.
//
// Per attempt rather than per job, so two runners either side of a leadership
// flap cannot interleave into one another's rows, and a retry does not
// overwrite the previous run's logs. Durable when called, so a build that
// crashes still has the logs it had got to — v1's log file only reached IPFS
// after the run returned.
type attemptSink struct {
	s       *Service
	id      string
	attempt uint32
}

func (k *attemptSink) Log(_ context.Context, at time.Time, cid string) error {
	if cid == "" {
		return errors.New("build: a log entry needs a cid")
	}
	return k.write(attemptLogKey(k.id, k.attempt, at), cid)
}

func (k *attemptSink) Asset(_ context.Context, resourceID, cid string) error {
	if resourceID == "" {
		return errors.New("build: an asset needs a resource id")
	}
	if cid == "" {
		return errors.New("build: an asset needs a cid")
	}
	return k.write(attemptAssetKey(k.id, k.attempt, resourceID), cid)
}

func (k *attemptSink) write(key, cid string) error {
	if err := k.s.store.Batch([]raft.BatchOp{set(key, []byte(cid))}, k.s.cfg.Queue.ApplyTimeout); err != nil {
		return fmt.Errorf("build: recording %q: %w", key, err)
	}
	return nil
}
