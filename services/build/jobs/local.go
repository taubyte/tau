package jobs

import (
	"context"
	"errors"
	"io"

	"github.com/taubyte/tau/core/service"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/services/build/run"
)

// Local is jobs as build's own routes ask them in the same process: what an
// RPC does once its peer was allowed, with no peer to allow, since the caller is
// this node's own ingress, which authorized the person asking.
type Local struct{ s *server }

// Local is this node's jobs, in-process.
func (b *hosted) Local() Local { return Local{s: b.server} }

func (l Local) Enqueue(ctx context.Context, repositoryKey, ref, commitSHA string) (string, error) {
	return l.s.enqueue(ctx, &buildv2.PushEvent{RepositoryKey: repositoryKey, Ref: ref, CommitSha: commitSHA})
}

func (l Local) Get(_ context.Context, jobID string) (*buildv2.Job, error) { return l.s.job(jobID) }

// Await is jobID once it has ended, waited for on this node's ring, which a job
// ending here rings: where it is claimed, which is this node while it leads.
func (l Local) Await(ctx context.Context, jobID string) (*buildv2.Job, error) {
	for {
		rung := l.s.runs.wait()
		job, err := l.s.job(jobID)
		if err != nil {
			return nil, err
		}
		if finished(job.GetStatus()) {
			return job, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-rung:
		}
	}
}

func (l Local) List(_ context.Context, projectID string, emit func(jobID string) error) error {
	return l.s.list(projectID, func(ref *buildv2.JobRef) error { return emit(ref.GetId()) })
}

func (l Local) Cancel(_ context.Context, jobID string) error { return l.s.cancel(jobID) }

func (l Local) Retry(_ context.Context, jobID string) error { return l.s.retry(jobID) }

func (l Local) Follow(ctx context.Context, jobID string, attempt int32, from int64, emit func(*buildv2.Job_Event) error) (int64, error) {
	return l.followFrom(ctx, jobID, attempt, from, false, emit)
}

func (l Local) Snapshot(ctx context.Context, jobID string, attempt int32, from int64, emit func(*buildv2.Job_Event) error) (int64, error) {
	return l.followFrom(ctx, jobID, attempt, from, true, emit)
}

// followFrom is a follow from from, answering the offset the last event it emitted ended
// at, as a follow over the wire does.
func (l Local) followFrom(ctx context.Context, jobID string, attempt int32, from int64, snapshot bool, emit func(*buildv2.Job_Event) error) (int64, error) {
	offset := from
	err := l.s.follow(ctx, &buildv2.FollowRequest{JobId: jobID, Attempt: attempt, Offset: from, Snapshot: snapshot}, func(ev *buildv2.Job_Event) error {
		if err := emit(ev); err != nil {
			return err
		}
		offset = ev.GetOffset()
		return nil
	})
	return offset, err
}

// Direct is build's jobs with no queue to share: a push runs to its end in the
// caller awaiting it, its plan and then each environment's build, recorded as a
// queued one is. It is what a development cloud with no build node builds with
// (instance/dream). Its queue is its own, in memory.
type Direct struct {
	Local
	also []io.Closer
}

// Alone is jobs on the instance h, run with r: the records in the database a build
// node keeps them in, so whatever reads a build's records reads these.
func Alone(h service.Host, r run.Runner) (*Direct, error) {
	s, also, err := onInstance(h, Config{}, raft.NewMockCluster(), withRunner(r))
	d := &Direct{Local: Local{s: s}, also: also}
	if err != nil {
		return nil, errors.Join(err, d.Close())
	}
	return d, nil
}

// Await runs the queue in the caller until jobID has ended: each step claims what
// is due and runs it here, so a plan's environment jobs run as it queues them.
func (d *Direct) Await(ctx context.Context, jobID string) (*buildv2.Job, error) {
	for {
		job, err := d.s.job(jobID)
		if err != nil {
			return nil, err
		}
		if finished(job.GetStatus()) {
			return job, nil
		}
		ran, err := d.s.step(ctx)
		if err != nil {
			return nil, err
		}
		if !ran {
			return nil, errFailedPrecondition("build: job %s is %s and nothing is due to run", jobID, job.GetStatus())
		}
	}
}

// Close closes what Alone opened.
func (d *Direct) Close() error {
	var errs []error
	for i := len(d.also) - 1; i >= 0; i-- {
		errs = append(errs, d.also[i].Close())
	}
	d.also = nil
	return errors.Join(errs...)
}
