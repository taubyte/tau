package hosted

import (
	"context"

	"connectrpc.com/connect"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/services/build/backend"
	"github.com/taubyte/tau/services/build/clients"
)

// routed is the job API as build's routes ask it: the control on this node, which
// answers when it leads the queue, else the other build nodes over p2p. Unavailable
// is the control's word for "not the leader", and the one that sends a call on.
type routed struct {
	here, elsewhere jobs
}

// jobs is the job API as build's routes ask it, here or over p2p.
type jobs interface {
	Enqueue(ctx context.Context, repositoryKey, ref, commitSHA string) (string, error)
	Get(ctx context.Context, jobID string) (*buildv2.Job, error)
	List(ctx context.Context, projectID string, emit func(jobID string) error) error
	Cancel(ctx context.Context, jobID string) error
	Retry(ctx context.Context, jobID string) error
	Follow(ctx context.Context, jobID string, attempt int32, from int64, emit func(*buildv2.Job_Event) error) (int64, error)
	Snapshot(ctx context.Context, jobID string, attempt int32, from int64, emit func(*buildv2.Job_Event) error) (int64, error)
}

var (
	_ jobs = backend.Local{}
	_ jobs = (*clients.BuildClient)(nil)
)

func elsewhere(err error) bool { return connect.CodeOf(err) == connect.CodeUnavailable }

func (r routed) Enqueue(ctx context.Context, repositoryKey, ref, commitSHA string) (string, error) {
	id, err := r.here.Enqueue(ctx, repositoryKey, ref, commitSHA)
	if elsewhere(err) {
		return r.elsewhere.Enqueue(ctx, repositoryKey, ref, commitSHA)
	}
	return id, err
}

func (r routed) Get(ctx context.Context, jobID string) (*buildv2.Job, error) {
	job, err := r.here.Get(ctx, jobID)
	if elsewhere(err) {
		return r.elsewhere.Get(ctx, jobID)
	}
	return job, err
}

func (r routed) List(ctx context.Context, projectID string, emit func(jobID string) error) error {
	err := r.here.List(ctx, projectID, emit)
	if elsewhere(err) {
		return r.elsewhere.List(ctx, projectID, emit)
	}
	return err
}

func (r routed) Cancel(ctx context.Context, jobID string) error {
	err := r.here.Cancel(ctx, jobID)
	if elsewhere(err) {
		return r.elsewhere.Cancel(ctx, jobID)
	}
	return err
}

func (r routed) Retry(ctx context.Context, jobID string) error {
	err := r.here.Retry(ctx, jobID)
	if elsewhere(err) {
		return r.elsewhere.Retry(ctx, jobID)
	}
	return err
}

// Follow resumes elsewhere from where this node's left off, so nothing is said twice.
func (r routed) Follow(ctx context.Context, jobID string, attempt int32, from int64, emit func(*buildv2.Job_Event) error) (int64, error) {
	at, err := r.here.Follow(ctx, jobID, attempt, from, emit)
	if elsewhere(err) {
		return r.elsewhere.Follow(ctx, jobID, attempt, at, emit)
	}
	return at, err
}

func (r routed) Snapshot(ctx context.Context, jobID string, attempt int32, from int64, emit func(*buildv2.Job_Event) error) (int64, error) {
	at, err := r.here.Snapshot(ctx, jobID, attempt, from, emit)
	if elsewhere(err) {
		return r.elsewhere.Snapshot(ctx, jobID, attempt, at, emit)
	}
	return at, err
}
