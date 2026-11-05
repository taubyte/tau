package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	builderv2 "github.com/taubyte/tau/pkg/proto/gen/tau/builder/v2"
	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
	"github.com/taubyte/tau/pkg/queue"
	"github.com/taubyte/tau/services/plm/run"
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

	// Each job goes to a builder, as many at once as there are builders free.
	handed, err := s.dispatch(ctx)
	return drained || handed, err
}

// run executes one claimed delivery on the builder id, r. A cancel, or the claim's
// deadline, stops the run at its builder, and the run is still awaited, so what it
// built and said comes back to be settled.
func (s *server) run(ctx context.Context, d queue.Delivery, id peer.ID, r run.Builder) error {
	deadline := s.claimDeadline(d)

	// The run's context exists before the record is claimed, since begin registers its
	// cancel inside the claiming section. The bound is a duration off this service's
	// clock, because WithDeadline compares against the wall clock.
	runCtx, stop := context.WithTimeout(ctx, deadline.Sub(s.now()))
	defer stop()

	// Both acks are inside the sections that decided them (begin's drop, settle's);
	// the build in between holds no lock.
	rec, claimed, err := s.awaitBegin(runCtx, d, deadline, stop, id)
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
		Prelude:       rec.Prelude,
		Within:        deadline.Sub(s.now()),
	}
	which := &builderv2.Run{JobId: rec.ID, Attempt: rec.Attempt}
	defer context.AfterFunc(runCtx, func() { r.Cancel(context.WithoutCancel(ctx), which) })()

	// The build minute starts after the claim, before the run.
	started := s.now()

	if rec.Environment == "" {
		return s.plan(ctx, d, rec, work, started, r)
	}

	built, err := r.Build(ctx, work)
	if err := s.abandoned(ctx, r, which, err, built.GetError() != ""); err != nil {
		return fmt.Errorf("job %s abandoned: %w", rec.ID, err)
	}
	var runErr error
	if msg := built.GetError(); msg != "" {
		runErr = errors.New(msg)
	}
	p, err := s.produced(ctx, rec, built.GetStored())
	if err != nil && runErr == nil {
		runErr = err
	}
	s.accountBuild(ctx, rec, started, runErr)
	return s.settle(ctx, d, r, rec, p, runErr)
}

// produced is what a run stored, as the record files it, each artifact accounted
// where it was stored: a cid says nothing about size.
func (s *server) produced(ctx context.Context, rec jobRecord, stored []*builderv2.Stored) (produced, error) {
	p := produced{attempt: rec.Attempt, assets: make(map[assetRef]string, len(stored))}
	for _, kept := range stored {
		if kept.GetResourceId() == "" || kept.GetCid() == "" {
			return p, errors.New("build: a stored artifact names its resource and its cid")
		}
		p.assets[assetRef{environment: rec.Environment, resource: kept.GetResourceId()}] = kept.GetCid()
		s.emit(ctx, Accounted{
			Project:     rec.ProjectID,
			Environment: rec.Environment,
			Job:         rec.ID,
			Attempt:     rec.Attempt,
			Op:          OpArtifact,
			Bytes:       kept.GetBytes(),
		})
	}
	return p, nil
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
func (s *server) begin(d queue.Delivery, deadline time.Time, stop context.CancelFunc, last bool, builder peer.ID) (rec jobRecord, running, missing bool, err error) {
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
			return j.writes().settled(plmv2.JobStatus_JobStatusFailed, orphanReason(s.cfg.RecordReads), produced{}).
				retained().commit("failing")
		}
		// Cancelled is one of the three terminal states, so this is the cancel
		// case as much as it is the redelivery case.
		if j.rec().terminal() {
			return j.ack(s.work, d)
		}

		// The entry's key is the section's subject, not the record's id.
		taken := j.writes().claimed(j.rec().Attempt+1, deadline, builder.String())
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
func (s *server) awaitBegin(ctx context.Context, d queue.Delivery, deadline time.Time, stop context.CancelFunc, builder peer.ID) (jobRecord, bool, error) {
	for read := 1; ; read++ {
		rec, running, missing, err := s.begin(d, deadline, stop, read >= s.cfg.RecordReads, builder)
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

// plan runs a plan job: which environments the commit feeds, one job enqueued for
// each, then the plan settled as their parent. The environment jobs are created
// between its two sections, each in its own, like a push's: a section writes only
// the job it read. Creating one is idempotent, so a plan run twice makes them once.
// What the plan said opens each environment job's log; the plan's own is sealed
// with how it ended, and kept by nobody.
func (s *server) plan(ctx context.Context, d queue.Delivery, rec jobRecord, work run.Work, started time.Time, r run.Builder) error {
	which := &builderv2.Run{JobId: rec.ID, Attempt: rec.Attempt}
	planned, err := r.Plan(ctx, work)
	if err := s.abandoned(ctx, r, which, err, planned.GetError() != ""); err != nil {
		return fmt.Errorf("job %s abandoned: %w", rec.ID, err)
	}
	var runErr error
	if msg := planned.GetError(); msg != "" {
		runErr = errors.New(msg)
	}
	said := planned.GetSaid()
	s.accountBuild(ctx, rec, started, runErr)

	wanted, err := s.taken(d)
	if err != nil || !wanted {
		s.discard(ctx, r, which)
		return err
	}

	// WHAT PLM DID WITH THE PLAN closes its log: the jobs it queued, or why it
	// failed it.
	var (
		children []string
		left     plmv2.JobStatus
		lines    []string
	)
	if runErr == nil {
		if len(planned.GetEnvironments()) == 0 {
			runErr = errors.New("the commit feeds no environment")
			lines = append(lines, "error: "+runErr.Error())
		}
		for _, environment := range planned.GetEnvironments() {
			id, err := s.createEnvironment(rec, environment, said)
			if err != nil {
				runErr = fmt.Errorf("enqueueing %s: %w", environment, err)
				lines = append(lines, "error: "+runErr.Error())
				break
			}
			children = append(children, id)
			lines = append(lines, fmt.Sprintf("queued job %s for %s", id, environment))
		}
	}

	err = s.withJob(d.ID, func(j *held) error {
		if !j.found() {
			return nil
		}
		left = j.rec().status()
		if j.rec().terminal() {
			return nil
		}
		if j.rec().status() != plmv2.JobStatus_JobStatusClaimed {
			return j.requeue()
		}
		if runErr != nil {
			left = plmv2.JobStatus_JobStatusFailed
			return j.fail(strings.TrimSpace(strings.TrimSpace(said)+"\nerror: "+runErr.Error()), produced{})
		}
		left = plmv2.JobStatus_JobStatusSuccess
		return j.planned(children)
	})
	if _, sealErr := s.seal(ctx, r, &builderv2.Sealing{Run: which, Status: left, Said: lines}); err == nil {
		err = sealErr
	}
	return err
}

// taken is settling's first section: the ack, which is the commit point, and
// whether what the run made is still wanted. A record gone, or neither terminal nor
// this run's claim, wants nothing: a reopened job's item goes back.
func (s *server) taken(d queue.Delivery) (wanted bool, err error) {
	err = s.withJob(d.ID, func(j *held) error {
		if err := j.ack(s.work, d); err != nil {
			return err
		}
		if !j.found() {
			return nil
		}
		if !j.rec().terminal() && j.rec().status() != plmv2.JobStatus_JobStatusClaimed {
			return j.requeue()
		}
		wanted = true
		return nil
	})
	return wanted, err
}

// abandoned is a run's call that did not get an answer about the run: the builder
// gone, the run turned down, or this node shutting down while the run failed, which
// may be the shutdown's doing however the builder answered. The delivery is
// abandoned rather than the job failed: left unacked, the claim expires and the
// queue redelivers. A run that succeeded is settled even on the way out. Nothing will
// name what an abandoned run wrote, so its log is discarded.
func (s *server) abandoned(ctx context.Context, r run.Builder, which *builderv2.Run, err error, failed bool) error {
	shutdown := errors.Is(context.Cause(ctx), errShuttingDown)
	switch {
	case shutdown && (err != nil || failed):
		err = errShuttingDown
	case err != nil:
		err = fmt.Errorf("%w: %v", errBuilderLost, err)
	default:
		return nil
	}
	s.discard(ctx, r, which)
	return err
}

// discard ends a run's log at its builder without keeping it.
func (s *server) discard(ctx context.Context, r run.Builder, which *builderv2.Run) {
	s.seal(ctx, r, &builderv2.Sealing{Run: which, Discard: true})
}

// seal ends a run's log at its builder, its last event the status its job was left
// in. It is bounded by PublishTimeout off a context the run's cancellation cannot
// reach. A store that would not take the log costs the log, never the job; a log
// the builder could not hand over whole is an error, and named by nothing.
func (s *server) seal(ctx context.Context, r run.Builder, sealing *builderv2.Sealing) (*builderv2.Sealed, error) {
	ctx, stop := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.PublishTimeout)
	defer stop()
	sealed, err := r.Seal(ctx, sealing)
	if err != nil {
		return &builderv2.Sealed{}, err
	}
	return sealed, nil
}

// settle acks, writes the record, then has the builder seal the log and files its
// cid: three sections with the network call between the last two.
//
// The ack is the commit point: a node whose claim expired or which lost leadership
// learns it in the first section and publishes nothing. The second section reads
// again, since the record may have become terminal (keep the status, file what was
// built), stayed Claimed by this run, or been reopened by a cancel and retry (put
// the item back). What it leaves the job as is the log's last record, so the kept
// log is everything that happened to the run, its end included.
//
// The seal is outside every section because jobLock covers the whole job table and
// a slow builder or asset store would stall every claim, Enqueue and Cancel on the
// node; the third section files the log's cid. A log that could not be kept is lost
// and says so; the job's status does not hang on it.
func (s *server) settle(ctx context.Context, d queue.Delivery, r run.Builder, rec jobRecord, p produced, runErr error) error {
	which := &builderv2.Run{JobId: rec.ID, Attempt: rec.Attempt}
	wanted, err := s.taken(d)
	if err != nil || !wanted {
		s.discard(ctx, r, which)
		return err
	}

	var (
		settled bool
		left    plmv2.JobStatus
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
		if j.rec().status() != plmv2.JobStatus_JobStatusClaimed {
			// The reopened job keeps this run's output, filed under the run's attempt; its
			// status is untouched. Record first, push second.
			left = j.rec().status()
			if err := j.logged(p); err != nil {
				return err
			}
			return j.requeue()
		}

		if runErr != nil {
			// AN ERROR FROM A RUN IS TERMINAL. The queue's retry exists for a builder
			// that DIED — which is a claim that expired, not an error returned — and
			// retrying a compile error five times is waste.
			left = plmv2.JobStatus_JobStatusFailed
			return j.fail(runErr.Error(), p)
		}

		// The ref anchor moves in the same entry as the success: this commit is what the
		// next build of this ref diffs from.
		left = plmv2.JobStatus_JobStatusSuccess
		return j.succeed(p)
	})
	if err != nil || !settled {
		s.discard(ctx, r, which)
		return err
	}

	// OUTSIDE EVERY SECTION, holding nothing. Past the record, so the log ends with
	// what it left the job as.
	sealed, err := s.seal(ctx, r, &builderv2.Sealing{Run: which, Status: left})
	if err != nil {
		return errInternal("build: job %s: sealing its log: %v", rec.ID, err)
	}
	if sealed.GetLog() == "" {
		return nil
	}
	s.emit(ctx, Accounted{
		Project:     rec.ProjectID,
		Environment: rec.Environment,
		Job:         rec.ID,
		Attempt:     rec.Attempt,
		Op:          OpLog,
		Bytes:       sealed.GetBytes(),
	})
	return s.withJob(d.ID, func(j *held) error {
		if !j.found() {
			return nil
		}
		return j.logged(produced{attempt: p.attempt, logCID: sealed.GetLog()})
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
// to diff against and the Builder must treat it as a full build — exactly what a
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
			if err := j.finish(plmv2.JobStatus_JobStatusFailed, "attempts exhausted"); err != nil {
				return err
			}
		}
		if err := j.ack(s.dead, d); err != nil {
			return fmt.Errorf("dead letter %w", err)
		}
		return nil
	})
}
