package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/queue"
	"github.com/taubyte/tau/services/build/run"
)

// step performs one cycle of the claim loop, which Start calls on a ticker; tests
// call it synchronously with an injected clock. It reports whether it did
// anything.
func (s *server) step(ctx context.Context) (bool, error) {
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

	// WITH NO RUNNER OF ITS OWN this node hands each job to a runner that opened a
	// stream to it, as many at once as there are runners free.
	if s.runner == nil {
		return drained, s.dispatch(ctx)
	}

	d, err := s.work.Claim()
	if errors.Is(err, queue.ErrNoItem) {
		return drained, nil
	}
	if err != nil {
		return drained, fmt.Errorf("claim: %w", err)
	}

	return true, s.run(ctx, d, s.runner)
}

// run executes one claimed delivery on r: this node's own runner, or a runner's
// stream (dispatch.go).
func (s *server) run(ctx context.Context, d queue.Delivery, r run.Runner) error {
	deadline := s.claimDeadline(d)

	// The run's context exists before the record is claimed, since begin registers its
	// cancel inside the claiming section. The bound is a duration off this service's
	// clock, because WithDeadline compares against the wall clock.
	runCtx, stop := context.WithTimeout(ctx, deadline.Sub(s.now()))
	defer stop()

	// Both acks are inside the sections that decided them (begin's drop, settle's);
	// the build in between holds no lock.
	rec, claimed, err := s.awaitBegin(runCtx, d, deadline, stop)
	if err != nil || !claimed {
		return err
	}
	defer s.forget(d.ID)

	work := run.Work{
		JobID:         rec.ID,
		RepositoryKey: rec.RepositoryKey,
		Ref:           rec.Ref,
		CommitSHA:     rec.CommitSHA,
		Before:        s.lastCommit(rec.RepositoryKey, rec.Ref, rec.Environment),
		ProjectID:     rec.ProjectID,
		Attempt:       rec.Attempt,
		Environment:   rec.Environment,
	}

	// The build minute starts after the claim, before the run.
	started := s.now()

	if rec.Environment == "" {
		return s.plan(ctx, runCtx, d, rec, work, started, r)
	}

	out, runErr := s.invoke(runCtx, work, rec.Prelude, r)
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
	// failure at shutdown also costs only a redelivery. A runner that went away is
	// abandoned the same way. A run that returned nil is settled.
	if runErr != nil && errors.Is(context.Cause(ctx), errShuttingDown) {
		return fmt.Errorf("job %s abandoned: %w", rec.ID, errShuttingDown)
	}
	if errors.Is(runErr, errRunnerLost) {
		return fmt.Errorf("job %s abandoned: %w", rec.ID, runErr)
	}

	// Nothing is published for an abandoned delivery, which writes no record. settle
	// publishes after its ack.
	s.accountBuild(ctx, rec, started, runErr)
	return s.settle(ctx, d, out, runErr)
}

// accountBuild emits what one run occupied a builder for, before settle, so a slow
// meter cannot stall a section. Failed builds are accounted too.
func (s *server) accountBuild(ctx context.Context, rec jobRecord, started time.Time, runErr error) {
	s.emit(ctx, Accounted{
		Project:    rec.ProjectID,
		Repository: rec.RepositoryKey,
		// The environment a run built, and none for a plan, which builds nothing.
		Environment: rec.Environment,
		Job:         rec.ID,
		Attempt:     rec.Attempt,
		Op:          OpBuild,
		Duration:    s.now().Sub(started),
		Succeeded:   runErr == nil,
	})
}

// begin is the claim's transition, one section: the barrier, the re-read, then
// either the entry writing the record Claimed plus the registration that lets
// Cancel stop the run, or the ack that drops the delivery. The drop's ack is here
// because an ack outside the section could destroy a concurrent reopening's
// deduplicated item. Re-reading alone is not enough: without the section, a
// cancel between read and entry would be overwritten with Claimed. Not claimed is
// not an error.
func (s *server) begin(d queue.Delivery, deadline time.Time, stop context.CancelFunc, last bool) (rec jobRecord, running, missing bool, err error) {
	var claimed jobRecord

	err = s.withJob(d.ID, func(j *held) error {
		// A JOB WHOSE EVENTS HAVE NOT REACHED THIS BUILDER is read again (awaitBegin),
		// and after the last read failed by name: its item is acked, so it is not
		// claimed forever, and the failure is a job a user can find and retry.
		if !j.found() {
			if !last {
				missing = true
				return nil
			}
			if err := j.ack(s.work, d); err != nil {
				return err
			}
			return j.ended(j.writes().settled(buildv2.JobStatus_JobStatusFailed, orphanReason(s.cfg.RecordReads), produced{}).
				retained().commit("failing"))
		}
		// Cancelled is one of the three terminal states, so this is the cancel
		// case as much as it is the redelivery case.
		if j.rec().terminal() {
			return j.ack(s.work, d)
		}

		// The entry's key is the section's subject, not the record's id.
		taken := j.writes().claimed(j.rec().Attempt+1, deadline)
		if err := taken.commit("claiming"); err != nil {
			return err
		}

		// The registration is the body's last act; a refused one throws the claim away.
		claimed, running = taken.rec, true
		return j.register(stop)
	})
	if err != nil {
		return jobRecord{}, false, false, err
	}
	return claimed, running, missing, nil
}

// awaitBegin is begin, read again while the job is missing. THE CLAIM CAME
// THROUGH RAFT AND THE JOB THROUGH KVDB, which can lag, so a missing job is read
// RecordReads times at most, RecordReadInterval apart, and the last read decides.
func (s *server) awaitBegin(ctx context.Context, d queue.Delivery, deadline time.Time, stop context.CancelFunc) (jobRecord, bool, error) {
	for read := 1; ; read++ {
		rec, running, missing, err := s.begin(d, deadline, stop, read >= s.cfg.RecordReads)
		if err != nil || !missing {
			return rec, running, err
		}
		select {
		case <-ctx.Done():
			return jobRecord{}, false, ctx.Err()
		case <-time.After(s.cfg.RecordReadInterval):
		}
	}
}

// forget drops this node's cancel for a finished run; it needs no reading since the
// entry it drops is always this goroutine's.
func (s *server) forget(id string) {
	s.runningMu.Lock()
	delete(s.running, id)
	s.runningMu.Unlock()
}

// invoke opens the run's output and runs the job, returning the output even when
// the run failed: those are the lines somebody wants.
func (s *server) invoke(ctx context.Context, work run.Work, prelude string, r run.Runner) (out *runOutput, err error) {
	if out, err = s.output(work); err != nil {
		return nil, err
	}
	if prelude != "" {
		if _, err := io.WriteString(out, prelude); err != nil {
			return out, err
		}
	}

	// A panicking runner is a failed build, not a dead loop or a lingering claim. Why a
	// run failed is the last line of its log, since the record carries no reason.
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("runner panicked: %v", p)
		}
		if err != nil {
			fmt.Fprintf(out, "error: %v\n", err)
		}
	}()

	return out, r.Run(ctx, work, out)
}

// maxPrelude bounds what a plan says, which every environment job's record carries.
const maxPrelude = 64 << 10

// plan runs a plan job: which environments the commit feeds, one job enqueued for
// each, then the plan settled as their parent. The environment jobs are created
// between its two sections, each in its own, like a push's: a section writes only
// the job it read. Creating one is idempotent, so a plan run twice makes them once.
func (s *server) plan(ctx, runCtx context.Context, d queue.Delivery, rec jobRecord, work run.Work, started time.Time, r run.Runner) error {
	// A PLAN'S LOG IS LIVE ONLY: what it said, which jobs it queued and how it ended,
	// for whoever follows it while it plans. What it said also opens each environment
	// job's. A plan that could not open one plans unfollowed.
	said := &capped{max: maxPrelude}
	var live io.Writer = said
	out, err := s.output(work)
	if err == nil {
		live = io.MultiWriter(said, out)
	}
	defer out.discard()

	environments, runErr := s.planned(runCtx, work, live, r)
	if runErr != nil && errors.Is(context.Cause(ctx), errShuttingDown) {
		return fmt.Errorf("job %s abandoned: %w", rec.ID, errShuttingDown)
	}
	if errors.Is(runErr, errRunnerLost) {
		return fmt.Errorf("job %s abandoned: %w", rec.ID, runErr)
	}
	s.accountBuild(ctx, rec, started, runErr)

	var wanted bool
	err = s.withJob(d.ID, func(j *held) error {
		if err := j.ack(s.work, d); err != nil {
			return err
		}
		if !j.found() {
			return nil
		}
		if !j.rec().terminal() && j.rec().status() != buildv2.JobStatus_JobStatusClaimed {
			return j.requeue()
		}
		wanted = true
		return nil
	})
	if err != nil || !wanted {
		return err
	}

	var children []string
	if runErr == nil {
		if len(environments) == 0 {
			runErr = errors.New("the commit feeds no environment")
		}
		for _, environment := range environments {
			id, err := s.createEnvironment(rec, environment, said.String())
			if err != nil {
				runErr = fmt.Errorf("enqueueing %s: %w", environment, err)
				break
			}
			children = append(children, id)
			if out != nil {
				fmt.Fprintf(out, "queued job %s for %s\n", id, environment)
			}
		}
	}
	if runErr != nil && out != nil {
		fmt.Fprintf(out, "error: %v\n", runErr)
	}

	var (
		left    buildv2.JobStatus
		settled bool
	)
	err = s.withJob(d.ID, func(j *held) error {
		if !j.found() {
			return nil
		}
		left, settled = j.rec().status(), true
		if j.rec().terminal() {
			return nil
		}
		if j.rec().status() != buildv2.JobStatus_JobStatusClaimed {
			return j.requeue()
		}
		if runErr != nil {
			left = buildv2.JobStatus_JobStatusFailed
			return j.fail(strings.TrimSpace(said.String()+"\nerror: "+runErr.Error()), produced{})
		}
		left = buildv2.JobStatus_JobStatusSuccess
		return j.planned(children)
	})
	if err == nil && settled {
		out.status(left)
	}
	return err
}

// planned runs the runner's plan; a panic is a failed plan, as for a build.
func (s *server) planned(ctx context.Context, work run.Work, out io.Writer, r run.Runner) (environments []string, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("runner panicked: %v", p)
		}
	}()
	return r.Plan(ctx, work, out)
}

// capped keeps what is written up to max bytes and drops the rest, saying so once.
type capped struct {
	max  int
	buf  strings.Builder
	full bool
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	} else if !c.full {
		c.full = true
		c.buf.WriteString("\n... the plan said more than this\n")
	}
	return len(p), nil
}

func (c *capped) String() string { return c.buf.String() }

// settle acks, writes the record, then hands off the log and files it: three
// sections with the network call between the last two.
//
// The ack is the commit point: a node whose claim expired or which lost leadership
// learns it in the first section and publishes nothing. The barrier is withJob's
// like every section's, and the ack an ordinary write after the read. The first
// section also asks whether the output is still wanted (record gone, or cancelled
// and retried mid-build).
//
// The second section reads again, since the record may have become terminal (keep
// the status, file what was built), stayed Claimed by this run, or been reopened by
// a cancel and retry (put the item back). What it leaves the job as is the log's
// last record, so the stored log is everything that happened to the run, its end
// included.
//
// The publish is outside every section because jobLock covers the whole job table
// and a slow asset store would stall every claim, Enqueue and Cancel on the node;
// the third section files the log's cid. A log that could not be stored is lost and
// says so; the job's status does not hang on it. A leadership loss between the two
// leaves an unnamed object (doc.go).
func (s *server) settle(ctx context.Context, d queue.Delivery, out *runOutput, runErr error) error {
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

	p := out.produced()
	var (
		left    buildv2.JobStatus
		settled bool
	)
	err = s.withJob(d.ID, func(j *held) error {
		if !j.found() {
			return nil
		}
		settled = true
		if j.rec().terminal() {
			// A cancelled job keeps its status and still gets what this run built.
			left = j.rec().status()
			return j.logged(p)
		}
		if j.rec().status() != buildv2.JobStatus_JobStatusClaimed {
			// The reopened job keeps this run's output, filed under the run's attempt; its
			// status is untouched. Record first, push second.
			left = j.rec().status()
			if err := j.logged(p); err != nil {
				return err
			}
			return j.requeue()
		}

		if runErr != nil {
			// AN ERROR FROM Run IS TERMINAL. The queue's retry exists for a
			// worker that DIED — which is a claim that expired, not an error
			// returned — and retrying a compile error five times is waste.
			left = buildv2.JobStatus_JobStatusFailed
			return j.fail(runErr.Error(), p)
		}

		// The ref anchor moves in the same entry as the success: this commit is what the
		// next build of this ref diffs from.
		left = buildv2.JobStatus_JobStatusSuccess
		return j.succeed(p)
	})
	if err != nil || !settled {
		return err
	}

	// OUTSIDE EVERY SECTION, holding nothing. Past the record, so the log ends with
	// what it left the job as.
	if err := out.status(left); err != nil {
		return errInternal("build: job %s: writing its end into its log: %v", d.ID, err)
	}
	cid, err := out.publish(ctx)
	if cid == "" {
		return err
	}
	return s.withJob(d.ID, func(j *held) error {
		if !j.found() {
			return nil
		}
		return j.logged(produced{attempt: p.attempt, logCID: cid})
	})
}

// claimDeadline is when this run must be over: the claim's deadline less the
// margin (a run reaching it is a second claimer), or BuildTimeout if smaller.
func (s *server) claimDeadline(d queue.Delivery) time.Time {
	lease := d.Deadline.Add(-s.cfg.ClaimMargin)
	policy := s.now().Add(s.cfg.BuildTimeout)
	if policy.Before(lease) {
		return policy
	}
	return lease
}

// lastCommit is the "before" of the next diff: the commit this service itself
// last FINISHED for this exact (repository, ref, environment). Absent means there is nothing
// to diff against and the Runner must treat it as a full build — exactly what a
// GitHub payload's all-zero `before` signals for a newly created branch.
func (s *server) lastCommit(repositoryKey, ref, environment string) string {
	ctx, stop := context.WithTimeout(context.Background(), s.cfg.Queue.ApplyTimeout)
	defer stop()
	// An anchor that cannot be read is a full build, which is correct, only slower.
	sha, _, err := s.journal.anchor(ctx, repositoryKey, ref, environment)
	if err != nil {
		return ""
	}
	return sha
}

// drainDeadLetter marks one exhausted job Failed and acks it. The claim is outside
// the section; the read, record and ack inside, so a cancel cannot be overwritten
// with Failed.
func (s *server) drainDeadLetter() (bool, error) {
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
