package hosted

import (
	"context"
	"time"

	"connectrpc.com/connect"

	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
	"github.com/taubyte/tau/services/plm/clients"
	"github.com/taubyte/tau/services/plm/jobs"
)

// routed is the job API as plm's routes ask it: the control on this node, which
// answers when it leads the queue, else the other plm nodes over p2p. Unavailable
// is the control's word for "not the leader", and the one that sends a call on.
type routed struct {
	here, elsewhere jobAPI
	queue           electing
}

// electing is the queue this node's jobs live in, as routing asks it.
type electing interface {
	WaitForLeader(ctx context.Context) error
}

// electionWait bounds how long a call nobody answered waits for this node's queue
// to elect.
const electionWait = 30 * time.Second

// again is whether a call this node deferred and no other plm node answered is
// asked here once more: a cloud's only plm node defers until its group elects, and
// with nobody to send the call to it would tell the first caller to come back. Once
// the group knows a leader, this node either is it or still defers.
func (r routed) again(ctx context.Context, err error) bool {
	if !elsewhere(err) {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, electionWait)
	defer cancel()
	return r.queue.WaitForLeader(ctx) == nil
}

// jobAPI is the job API as plm's routes ask it, here or over p2p.
type jobAPI interface {
	Enqueue(ctx context.Context, repositoryKey, ref, commitSHA string) (string, error)
	Get(ctx context.Context, jobID string) (*plmv2.Job, error)
	List(ctx context.Context, projectID string, emit func(jobID string) error) error
	Cancel(ctx context.Context, jobID string) error
	Retry(ctx context.Context, jobID string) error
	Follow(ctx context.Context, jobID string, attempt int32, from int64, emit func(*plmv2.Job_Event) error) (int64, error)
	Snapshot(ctx context.Context, jobID string, attempt int32, from int64, emit func(*plmv2.Job_Event) error) (int64, error)
}

var (
	_ jobAPI = jobs.Local{}
	_ jobAPI = (*clients.BuildClient)(nil)
)

func elsewhere(err error) bool { return connect.CodeOf(err) == connect.CodeUnavailable }

func (r routed) Enqueue(ctx context.Context, repositoryKey, ref, commitSHA string) (string, error) {
	id, err := r.here.Enqueue(ctx, repositoryKey, ref, commitSHA)
	if elsewhere(err) {
		if id, err = r.elsewhere.Enqueue(ctx, repositoryKey, ref, commitSHA); r.again(ctx, err) {
			return r.here.Enqueue(ctx, repositoryKey, ref, commitSHA)
		}
	}
	return id, err
}

func (r routed) Get(ctx context.Context, jobID string) (*plmv2.Job, error) {
	job, err := r.here.Get(ctx, jobID)
	if elsewhere(err) {
		if job, err = r.elsewhere.Get(ctx, jobID); r.again(ctx, err) {
			return r.here.Get(ctx, jobID)
		}
	}
	return job, err
}

func (r routed) List(ctx context.Context, projectID string, emit func(jobID string) error) error {
	err := r.here.List(ctx, projectID, emit)
	if elsewhere(err) {
		if err = r.elsewhere.List(ctx, projectID, emit); r.again(ctx, err) {
			return r.here.List(ctx, projectID, emit)
		}
	}
	return err
}

func (r routed) Cancel(ctx context.Context, jobID string) error {
	err := r.here.Cancel(ctx, jobID)
	if elsewhere(err) {
		if err = r.elsewhere.Cancel(ctx, jobID); r.again(ctx, err) {
			return r.here.Cancel(ctx, jobID)
		}
	}
	return err
}

func (r routed) Retry(ctx context.Context, jobID string) error {
	err := r.here.Retry(ctx, jobID)
	if elsewhere(err) {
		if err = r.elsewhere.Retry(ctx, jobID); r.again(ctx, err) {
			return r.here.Retry(ctx, jobID)
		}
	}
	return err
}

// Follow resumes elsewhere from where this node's left off, so nothing is said twice.
func (r routed) Follow(ctx context.Context, jobID string, attempt int32, from int64, emit func(*plmv2.Job_Event) error) (int64, error) {
	at, err := r.here.Follow(ctx, jobID, attempt, from, emit)
	if elsewhere(err) {
		if at, err = r.elsewhere.Follow(ctx, jobID, attempt, at, emit); r.again(ctx, err) {
			return r.here.Follow(ctx, jobID, attempt, at, emit)
		}
	}
	return at, err
}

func (r routed) Snapshot(ctx context.Context, jobID string, attempt int32, from int64, emit func(*plmv2.Job_Event) error) (int64, error) {
	at, err := r.here.Snapshot(ctx, jobID, attempt, from, emit)
	if elsewhere(err) {
		if at, err = r.elsewhere.Snapshot(ctx, jobID, attempt, at, emit); r.again(ctx, err) {
			return r.here.Snapshot(ctx, jobID, attempt, at, emit)
		}
	}
	return at, err
}
