package backend

import (
	"context"
	"errors"
	"fmt"
	"time"

	buildv1 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v1"
	"github.com/taubyte/tau/pkg/queue"
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

	// THE RUN'S OUTPUT IS A FILE AND A VALUE, and this is where the sink used
	// to be. A build streams rows while it runs, so the thing that took them
	// was the ONE WRITER IN THIS PACKAGE OUTSIDE EVERY SECTION — and three of
	// the last four escapes this package found came through it, each one
	// moving another repository's ref anchor on the happy path of every build.
	// What a run writes now is a log FILE (services/build/livelog) and a map in
	// this goroutine's own memory; the store hears about either of them once,
	// from settle, inside the section that read the record.
	// THE CLOCK STARTS WHERE THE BUILD DOES, after the claim and before the
	// run: what a build minute measures is what the builder was occupied for,
	// not what the queue held.
	started := s.now()

	out, runErr := s.invoke(runCtx, work)
	if out == nil {
		// The run never started: this node could not open a log for it. That
		// is a FAILED build rather than a build with no log — a node that
		// cannot record what a build printed should not be the node that runs
		// it, and settling it as failed is what hands the job to another one.
		s.accountBuild(ctx, rec, started, runErr)
		return s.settle(ctx, d, nil, runErr)
	}
	// Whatever happens below, the log is closed — which releases anybody
	// watching it — and the file goes. It is scratch: what outlives the run is
	// a cid, and only if it was published.
	defer out.discard()

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

	// AND NOTHING IS PUBLISHED ON THE WAY OUT, above, for the reason the ack is
	// the commit point: an abandoned delivery writes no record, so a log shipped
	// there would be bytes in the asset store that no job ever names, and
	// nothing in this design deletes those. The rebuild writes its own.
	//
	// THE OUTPUT IS HANDED OVER RATHER THAN PUBLISHED HERE, and that is the
	// same sentence applied to the two ways a delivery is lost that this
	// function cannot see coming. `settle(d, out.publish(ctx), err)` published
	// BEFORE it acked — Go evaluates the argument first — so a claim that had
	// expired and a leadership flap each shipped a whole log to the store and
	// then wrote no record naming it: the very orphan the paragraph above
	// declines to make, made on the two paths where nobody chose it. settle
	// publishes after its ack, on the branches that record what it returns.
	s.accountBuild(ctx, rec, started, runErr)
	return s.settle(ctx, d, out, runErr)
}

// accountBuild emits what one run occupied a builder for.
//
// IT IS EMITTED BEFORE THE SETTLE AND NOT INSIDE IT, which is the same rule
// every other write on this path follows: settle takes a section, and a sink is
// somebody else's code. A meter that blocked inside the section that holds a
// job would turn a slow biller into a stuck queue.
//
// A FAILED BUILD IS ACCOUNTED. It occupied a builder for exactly as long as one
// that worked, and a plan that charges differently for the two has to be able
// to tell them apart — which is what Succeeded is for, rather than this
// deciding on the biller's behalf by emitting nothing.
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

// begin is the claim's own transition, and it is ONE SECTION: the barrier, the
// re-read that decides whether this delivery is still worth running, and then
// EITHER the entry that writes the record Claimed and the registration that
// lets Cancel stop the run, OR the ack that drops the delivery.
//
// ONE READ, TWO BRANCHES, TWO DIFFERENT WRITES — and that is why the ack is
// here rather than in run. The drop branch's write IS the ack: the read said
// this job has finished, and acking is what that read decided. Made after the
// section closed it was a queue write nothing serialized against a push, and
// pkg/queue's Push deduplicates against the index row of a claimed item
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
		// a row at jobRecordKey(rec.ID) inside withJob(d.ID, …) until the
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

// invoke opens the run's output and runs the job. The context is the claim's,
// built by run and registered by begin, so that a cancel can never land between
// the record reading Claimed and there being something to stop.
//
// IT HANDS THE OUTPUT BACK EVEN WHEN THE RUN FAILED, and that is the point of
// the named return: a build that failed, was cancelled, or panicked printed
// exactly the lines that say why, and those are the lines somebody wants. v1
// publishes its log from a `defer` inside the run — so what the defer does not
// survive, the log does not survive either, and the temp file it had been
// writing is left in /tmp with nothing naming it.
func (s *Service) invoke(ctx context.Context, work Work) (out *runOutput, err error) {
	if out, err = s.output(work); err != nil {
		return nil, err
	}

	// A runner that panics must not take the loop down with it, and must not
	// leave the item claimed until its lease expires either: a panic is a
	// failed build, which is what the recovery turns it into. The output is
	// already in the named return by then, so the log the panicking build wrote
	// is published like any other.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("runner panicked: %v", r)
		}
	}()

	return out, s.runner.Run(ctx, work, out)
}

// settle ends the delivery and writes what the run decided — THE ACK, THEN THE
// HANDOFF OF THE LOG, THEN THE RECORD. Two sections with the network call
// BETWEEN them, and the order of those three is the whole of this function.
//
// ACK IS THE COMMIT POINT, and it comes before anything this run produced is
// published. It is the only compare-and-swap this service has: the queue
// refuses an ack whose delivery it no longer holds, so a node whose claim
// expired — or which lost leadership mid-run — learns it in the FIRST section
// and PUBLISHES NOTHING. Content-addressed rows already written are harmless; a
// pointer written by a node that no longer speaks for the group is not. v1
// asked whether it still owned the job only when REPORTING, after it had
// already stashed assets and pushed keys to tns.
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
// ordinary write inside the body. It buys the rule with no exception in it:
// every write in this package is made by the body that read the record.
//
// AND THE PUBLISH IS OUTSIDE BOTH OF THEM, WHICH IS WHY THERE ARE TWO. A
// section is jobLock, and jobLock is ONE LOCK OVER THE WHOLE JOB TABLE
// (section.go): every transition of every job on this node takes it — a claim,
// a settle, a Cancel, a Retry, an Enqueue's resume, the dead-letter drain,
// every row of a reconcile pass. runOutput.publish hands a whole log to the
// ASSET STORE, which is the only door in this package that leaves this machine
// and is bounded only by PublishTimeout. Held across it, one slow storage node
// stalls WEBHOOK INGESTION and every user-facing cancel and retry on this node
// for up to that timeout per settled build — and the timeout is spent off a
// context.WithoutCancel, so a shutdown cannot cut it short either. Measured:
// with the store blocked, an unrelated repository's Enqueue was still waiting
// after 300ms and completed 0.58ms after the store was released.
// section.go says what is deliberately outside a section — the build, and the
// round trip to auth — and this is the third: a round trip to storage.
//
// THE FIRST SECTION IS THE ACK AND ONE QUESTION: is what this run produced
// still wanted by anybody? It is not, on the two branches that record nothing —
// a job whose record is gone, and a job that was cancelled and retried while
// the build ran, whose new attempt this run knows nothing about. Publishing on
// either would put a log in the asset store that no record will ever name, and
// nothing in this service unreferences an asset. That is why the question is
// asked before the publish rather than after it.
//
// THE SECOND SECTION READS AGAIN, AND MUST. The publish takes as long as the
// asset store takes, and three different things can write the record in that
// time — the same three the single section already had to re-read for:
//
//   - terminal. A Cancel wrote it while the run was going, or while the log was
//     going to the store, and a cancelled job must not be overwritten with the
//     outcome of the run that was cancelled. Its log is still recorded: the cid
//     of what that run printed is the one thing whoever cancelled it wants.
//   - Claimed, by this run. The ordinary case, and the only one this run's
//     outcome describes.
//   - OPEN. Somebody cancelled the job and then retried it, so the record is a
//     NEW attempt — and the retry's push was deduplicated against the very
//     delivery the ack destroyed (pkg/queue: Push is a no-op for an id the
//     queue holds, Ack deletes that id's index row). Writing this run's outcome
//     over it tells the caller its retry failed with the error of the build it
//     cancelled; leaving it alone but saying nothing leaves a job that reads
//     Open with no item and, once finish has dropped its open row, nothing for
//     reconcile to walk either. So the item goes back, in this section, where
//     the reopening could not have raced it.
//
// AND THE PUBLISH IS BEFORE THE RECORD RATHER THAN AFTER IT, which is what
// keeps the second section's branch honest: A LOG THIS NODE COULD NOT READ BACK
// FAILS THE BUILD IT BELONGS TO, and a job whose outcome had already been
// written could only be told so by moving a terminal record — which is the one
// thing no transition here does.
//
// WHAT THE ORDER COSTS is one replicated entry — the second section's barrier —
// per settled build, and a WINDOW between the ack and the record write that is
// as wide as the publish. A node that stops leading inside it leaves an object
// in the asset store that no record names and a job reading Claimed with no
// item, which reconcile pushes back after its cutoff. doc.go names it under
// what is not covered at all, with its width; it is the same window the single
// section had, one apply wide, made as wide as a storage round trip. The trade
// is deliberate: the window costs one unreferenced object on a leadership flap,
// and the lock costs every job on the node on every slow store.
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
		if !j.rec().terminal() && j.rec().status() != buildv1.JobStatus_JobStatusClaimed {
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
		// A LOG THAT COULD NOT BE READ BACK FAILS THE BUILD IT BELONGS TO. It is
		// invoke's rule at the other end of the run — a node that cannot open a
		// log for a build is not the node that runs it — and the alternative is
		// the defect this came from: a job settling Success carrying Logs:
		// map[], with nothing anywhere saying that a log was lost. A run that
		// ALREADY failed keeps its own reason, which is the one its caller needs.
		runErr = logErr
	}

	return s.withJob(d.ID, func(j *held) error {
		if !j.found() {
			return nil
		}
		if j.rec().terminal() {
			// A CANCELLED JOB STILL GETS ITS LOG. The status is not touched —
			// a cancelled build must not be overwritten with the outcome of
			// the run that was cancelled — but the cid of what that run
			// printed is the one thing whoever cancelled it wants to read, and
			// v1 went to the trouble of a second RPC to carry it (monkey's
			// cancelHandler hands patrick the accumulated logs). Here it is a
			// field on the record this section already holds.
			//
			// AND A LOG THIS NODE COULD NOT READ BACK IS THE ONE FAILURE A
			// CANCELLED JOB CANNOT RECORD: the status is not this run's to
			// move, and a cancelled job that carries no log claims nothing
			// untrue. So what is dropped here is the log, not the outcome.
			return j.logged(p)
		}
		if j.rec().status() != buildv1.JobStatus_JobStatusClaimed {
			// THE REOPENED JOB KEEPS THIS RUN'S LOG, and that is the one thing
			// this branch does that the single section did not have to. The
			// first section found a record this run's outcome was about and the
			// log went to the store on the strength of it; a cancel and a retry
			// landing during the handoff make the record a NEW attempt, and
			// dropping the cid here would leave an object the store keeps and
			// nothing names. It is filed under the RUN'S attempt, not the
			// record's, so it sits beside whatever the rebuild writes rather
			// than on top of it — which is what produced carries its own
			// attempt for. The status is untouched: this run's outcome is not
			// this record's.
			//
			// RECORD FIRST, PUSH SECOND, like every other place the two appear
			// together (held.requeue).
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

		// The ref anchor is written in the SAME entry as the success, because
		// it is the same fact: this commit is what the next build of this ref
		// diffs from, and it is read from here rather than from a webhook
		// (build.proto: "read from its own job history, never from the
		// frontend"). It used to be handed in as an extra BatchOp this body
		// composed; it is a row of the entry now, built off the record the
		// entry writes, so the anchor a success can move is this job's ref.
		return j.succeed(p)
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
