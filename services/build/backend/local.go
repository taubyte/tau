package backend

import (
	"context"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
)

// Local is the backend as build's own routes ask it in the same process: what an
// RPC does once its peer was allowed, with no peer to allow, since the caller is
// this node's own ingress, which authorized the person asking.
type Local struct{ s *server }

// Local is this backend, in-process.
func (b *hosted) Local() Local { return Local{s: b.server} }

func (l Local) Enqueue(ctx context.Context, repositoryKey, ref, commitSHA string) (string, error) {
	return l.s.enqueue(ctx, &buildv2.PushEvent{RepositoryKey: repositoryKey, Ref: ref, CommitSha: commitSHA})
}

func (l Local) Get(_ context.Context, jobID string) (*buildv2.Job, error) { return l.s.job(jobID) }

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
