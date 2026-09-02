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
	// RE-READ THE RECORD BEFORE DOING ANYTHING. This is the whole of what makes
	// cancel a record write rather than a message, and it is also how the one
	// gap between an ack and a terminal record heals itself: a redelivered job
	// that is already finished is acked and dropped.
	rec, found, err := s.record(d.ID)
	if err != nil {
		return err
	}
	switch {
	case !found:
		// An item with no record cannot be identified, let alone built.
		return s.ack(d)
	case rec.status() == buildv1.JobStatus_JobStatusCancelled, rec.terminal():
		return s.ack(d)
	}

	deadline := s.claimDeadline(d)
	rec.Status = int32(buildv1.JobStatus_JobStatusClaimed)
	rec.Attempt++
	rec.UpdatedAt = s.now().Unix()
	rec.ClaimedUntil = deadline.UnixNano()
	rec.Holder = s.host.ID().String()

	value, err := rec.encode()
	if err != nil {
		return errInternal("encoding job %s: %v", rec.ID, err)
	}
	if err := s.store.Batch([]raft.BatchOp{set(jobRecordKey(rec.ID), value)}, s.cfg.Queue.ApplyTimeout); err != nil {
		return fmt.Errorf("claiming job %s: %w", rec.ID, err)
	}

	work := Work{
		JobID:         rec.ID,
		RepositoryKey: rec.RepositoryKey,
		Ref:           rec.Ref,
		CommitSHA:     rec.CommitSHA,
		Before:        s.lastCommit(rec.RepositoryKey, rec.Ref),
		ProjectID:     rec.ProjectID,
		Attempt:       rec.Attempt,
	}

	runErr := s.invoke(ctx, deadline, work)

	// ACK IS THE COMMIT POINT, and it comes before anything this run produced
	// is published. It is the only compare-and-swap this service has: the
	// queue refuses an ack whose delivery it no longer holds, so a node whose
	// claim expired — or which lost leadership mid-run — learns it here and
	// PUBLISHES NOTHING. Content-addressed rows already written are harmless;
	// a pointer written by a node that no longer speaks for the group is not.
	//
	// v1 asked whether it still owned the job only when REPORTING, after it had
	// already stashed assets and pushed keys to tns.
	if err := s.ack(d); err != nil {
		return err
	}

	return s.settle(rec.ID, runErr)
}

// invoke runs the job with the claim's deadline over it and this node's cancel
// registered, so Cancel can stop it in the same process that wrote the record.
func (s *Service) invoke(ctx context.Context, deadline time.Time, work Work) (err error) {
	runCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	s.runningMu.Lock()
	s.running[work.JobID] = cancel
	s.runningMu.Unlock()
	defer func() {
		s.runningMu.Lock()
		delete(s.running, work.JobID)
		s.runningMu.Unlock()
	}()

	// A runner that panics must not take the loop down with it, and must not
	// leave the item claimed until its lease expires either: a panic is a
	// failed build, which is what the recovery turns it into.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("runner panicked: %v", r)
		}
	}()

	return s.runner.Run(runCtx, work, &attemptSink{s: s, id: work.JobID, attempt: work.Attempt})
}

// settle writes the terminal record after the ack has committed. The record is
// re-read because a Cancel may have written it while the run was going, and a
// cancelled job must not be overwritten with the outcome of the run that was
// cancelled.
func (s *Service) settle(id string, runErr error) error {
	rec, found, err := s.record(id)
	if err != nil {
		return err
	}
	if !found || rec.terminal() {
		return nil
	}

	if runErr != nil {
		// AN ERROR FROM Run IS TERMINAL. The queue's retry exists for a worker
		// that DIED — which is a claim that expired, not an error returned —
		// and retrying a compile error five times is waste.
		return s.finish(rec, buildv1.JobStatus_JobStatusFailed, runErr.Error())
	}

	// The ref anchor is written in the SAME entry as the success, because it is
	// the same fact: this commit is what the next build of this ref diffs from,
	// and it is read from here rather than from a webhook (build.proto: "read
	// from its own job history, never from the frontend").
	return s.finish(rec, buildv1.JobStatus_JobStatusSuccess, "",
		set(refLastKey(rec.RepositoryKey, rec.Ref), []byte(rec.CommitSHA)))
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

// ack removes the item for good, and reports a delivery that is no longer ours
// as what it is.
func (s *Service) ack(d queue.Delivery) error {
	if err := s.work.Ack(d); err != nil {
		return fmt.Errorf("ack %s: %w", d.ID, err)
	}
	return nil
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
func (s *Service) drainDeadLetter() (bool, error) {
	d, err := s.dead.Claim()
	if errors.Is(err, queue.ErrNoItem) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("dead letter claim: %w", err)
	}

	rec, found, err := s.record(d.ID)
	if err != nil {
		return true, err
	}
	if found && !rec.terminal() {
		if err := s.finish(rec, buildv1.JobStatus_JobStatusFailed, "attempts exhausted"); err != nil {
			return true, err
		}
	}
	if err := s.dead.Ack(d); err != nil {
		return true, fmt.Errorf("dead letter ack %s: %w", d.ID, err)
	}
	return true, nil
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
