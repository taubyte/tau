package clients

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/p2p/rpc"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/build/v2/buildv2connect"
	"github.com/taubyte/tau/services/build"
)

// BuildClient is the frontend's whole view of the backend: build.proto's five
// RPCs, over build.Protocol, through the pool's one-peer-at-a-time loop.
//
// EVERY ONE OF THEM CAN COME BACK Unavailable AND THAT IS NOT AN ERROR YET.
// The backend answers Unavailable on a raft follower (its leader.go makes that
// one rule for reads and writes alike), and Pool.Do walks to the next peer on
// exactly that code — so a cloud whose leader is the third peer the resolver
// names costs two extra round trips and no special handling here. It is only
// after every peer has said it that ErrNoBackend comes back.
type BuildClient struct {
	pool *Pool
}

// Builds adapts a pool dialling build's backend.
func Builds(p *Pool) (*BuildClient, error) {
	if err := requireProtocol(p, build.Protocol, "builds"); err != nil {
		return nil, err
	}
	return &BuildClient{pool: p}, nil
}

// Enqueue records a verified push and returns the job id it became.
//
// The three arguments are the whole of what crosses this boundary
// (build.proto's PushEvent), and the frontend has already established that the
// caller may cause a job for this repository before calling. The id comes back
// because an operator tracing a delivery needs to know which job it became;
// it must not reach the webhook's response body, and webhook.go does not put
// it there.
func (c *BuildClient) Enqueue(ctx context.Context, repositoryKey, ref, commitSHA string) (string, error) {
	var id string
	err := c.pool.Do(ctx, func(client *rpc.Client) error {
		queue := buildv2connect.NewQueueServiceClient(client.HTTP(), client.URL())
		resp, err := queue.Enqueue(ctx, connect.NewRequest(&buildv2.PushEvent{
			RepositoryKey: repositoryKey,
			Ref:           ref,
			CommitSha:     commitSHA,
		}))
		if err != nil {
			return err
		}
		id = resp.Msg.GetId()
		return nil
	})
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", connect.NewError(connect.CodeInternal, errors.New("build: backend enqueued a job with no id"))
	}
	return id, nil
}

// Get reads one job record.
func (c *BuildClient) Get(ctx context.Context, jobID string) (*buildv2.Job, error) {
	var job *buildv2.Job
	err := c.pool.Do(ctx, func(client *rpc.Client) error {
		jobs := buildv2connect.NewJobServiceClient(client.HTTP(), client.URL())
		resp, err := jobs.Get(ctx, connect.NewRequest(&buildv2.JobRef{Id: jobID}))
		if err != nil {
			return err
		}
		job = resp.Msg
		return nil
	})
	if err != nil {
		return nil, err
	}
	return job, nil
}

// List hands each of a project's job ids to emit as it arrives.
//
// A CONSUMED STREAM CANNOT BE RETRIED, and that is the one thing this method
// has to get right. Pool.Do's contract is that Unavailable means "ask the next
// peer" — correct for a unary call, and wrong the moment an id has already
// been handed onwards, because the next peer would start its listing from the
// beginning and the caller would see the first ids twice. So once anything has
// been emitted, a failure is re-wrapped as a plain error: Pool.Do returns
// every code but Unavailable immediately, so the plain error stops the walk.
// A stream that broke before its first id is still safe to retry and is left
// as it came.
func (c *BuildClient) List(ctx context.Context, projectID string, emit func(jobID string) error) error {
	if emit == nil {
		return connect.NewError(connect.CodeInternal, errors.New("build: list needs somewhere to put the ids"))
	}

	var sent int
	return c.pool.Do(ctx, func(client *rpc.Client) error {
		jobs := buildv2connect.NewJobServiceClient(client.HTTP(), client.URL())
		stream, err := jobs.List(ctx, connect.NewRequest(&buildv2.ProjectRef{Id: projectID}))
		if err != nil {
			return c.resumable(sent, err)
		}
		defer stream.Close()

		for stream.Receive() {
			if err := emit(stream.Msg().GetId()); err != nil {
				// The caller's own failure — a broken HTTP connection, most
				// likely. It is never a routing problem, so it must not be
				// retried against another peer.
				return fmt.Errorf("build: listing %q: %w", projectID, err)
			}
			sent++
		}
		return c.resumable(sent, stream.Err())
	})
}

// resumable keeps err's connect code while nothing has been handed onwards,
// and strips it once something has.
//
// THE CAUSE IS FOLDED IN WITH %v AND NOT %w, and that is the whole mechanism
// rather than a slip: %w leaves connect.CodeOf finding the Unavailable
// underneath, which is precisely the code Pool.Do walks to the next peer on, so
// a wrapped error would still be retried and the caller would still see the
// first ids twice. Breaking the chain IS the statement — once an id has been
// delivered this is no longer a routable failure, and nothing downstream should
// be able to conclude that it is.
func (c *BuildClient) resumable(sent int, err error) error {
	if err == nil || sent == 0 {
		return err
	}
	return fmt.Errorf("build: job listing broke off after %d ids: %v", sent, err)
}

// Cancel stops a job and writes its terminal record.
func (c *BuildClient) Cancel(ctx context.Context, jobID string) error {
	return c.pool.Do(ctx, func(client *rpc.Client) error {
		jobs := buildv2connect.NewJobServiceClient(client.HTTP(), client.URL())
		_, err := jobs.Cancel(ctx, connect.NewRequest(&buildv2.JobRef{Id: jobID}))
		return err
	})
}

// Retry re-queues a finished job. It answers nothing but success or failure —
// the caller already sent the id, so there is nothing else this call can tell
// it (build.proto's Retry returns Empty for the same reason).
func (c *BuildClient) Retry(ctx context.Context, jobID string) error {
	return c.pool.Do(ctx, func(client *rpc.Client) error {
		jobs := buildv2connect.NewJobServiceClient(client.HTTP(), client.URL())
		_, err := jobs.Retry(ctx, connect.NewRequest(&buildv2.JobRef{Id: jobID}))
		return err
	})
}

// Follow streams one run's output to emit — the run `attempt` names, from
// `from` to the end of it — and returns the offset it reached.
//
// THE OFFSET IS WHAT MAKES A RETRY SAFE, and it is the whole difference from
// List above. List cannot be retried once it has handed an id onwards, because
// the next peer would start again from the beginning and the caller would see
// the same ids twice — so it strips the Unavailable code to stop the walk. Here
// the request carries the run and where in it the caller got to, so a walk to
// the next peer RESUMES that run rather than repeating it or reading another
// run's log from the same offset. The code is left alone deliberately: only the
// node writing the log can answer, every other one says Unavailable, and
// walking past them is exactly how the writer is found.
//
// SO A WALK THAT FINDS NO WRITER ENDS Unavailable, and that is a log still to
// come — the job is queued, or claimed and not writing yet — which the caller
// may ask for again. A run that has ended stops the walk at the leader with
// FailedPrecondition instead, since no node will write its log again: what it
// logged is what its record files under its attempt, if anything.
//
// An emit that fails is the CALLER's failure — a hung-up HTTP client, most
// likely — and is never a routing problem, so it stops the walk with its own
// error.
func (c *BuildClient) Follow(ctx context.Context, jobID string, attempt int32, from int64, emit func(data []byte, next int64) error) (int64, error) {
	return c.read(ctx, jobID, attempt, from, false, emit)
}

// Snapshot is Follow up to what the log holds when it is asked rather than to
// the end of the build, which goes on. It walks to the writer and resumes the
// way Follow does.
func (c *BuildClient) Snapshot(ctx context.Context, jobID string, attempt int32, from int64, emit func(data []byte, next int64) error) (int64, error) {
	return c.read(ctx, jobID, attempt, from, true, emit)
}

func (c *BuildClient) read(ctx context.Context, jobID string, attempt int32, from int64, snapshot bool, emit func(data []byte, next int64) error) (int64, error) {
	if emit == nil {
		return from, connect.NewError(connect.CodeInternal, errors.New("build: follow needs somewhere to put the output"))
	}

	offset := from
	err := c.pool.Do(ctx, func(client *rpc.Client) error {
		jobs := buildv2connect.NewJobServiceClient(client.HTTP(), client.URL())
		stream, err := jobs.Follow(ctx, connect.NewRequest(&buildv2.FollowRequest{
			JobId:    jobID,
			Attempt:  attempt,
			Offset:   offset,
			Snapshot: snapshot,
		}))
		if err != nil {
			return err
		}
		defer stream.Close()

		for stream.Receive() {
			msg := stream.Msg()
			if err := emit(msg.GetData(), msg.GetNextOffset()); err != nil {
				return fmt.Errorf("build: following %q: %w", jobID, err)
			}
			offset = msg.GetNextOffset()
		}
		return stream.Err()
	})
	return offset, err
}

// Artifact is the content id of what one resource of one deployment currently
// runs.
//
// IT IS THE READ A WORKER MAKES, and the only one in this client that is about
// a project rather than a job. A resource nothing has built comes back as a
// connect.CodeNotFound error, unchanged — the code is preserved rather than
// flattened, because "nothing has built this yet" is a wait and "the cloud is
// unreachable" is a retry, and they are the same string.
func (c *BuildClient) Artifact(ctx context.Context, project, environment, resource string) (string, error) {
	var cid string
	err := c.pool.Do(ctx, func(client *rpc.Client) error {
		jobs := buildv2connect.NewJobServiceClient(client.HTTP(), client.URL())
		resp, err := jobs.Artifact(ctx, connect.NewRequest(&buildv2.ArtifactRequest{
			Project:     project,
			Environment: environment,
			Resource:    resource,
		}))
		if err != nil {
			return err
		}
		cid = resp.Msg.GetCid()
		return nil
	})
	if err != nil {
		return "", err
	}
	return cid, nil
}
