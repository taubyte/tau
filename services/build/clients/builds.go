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

// BuildClient is the frontend's view of the backend: build.proto's five RPCs
// through the pool. A follower answers Unavailable and Pool.Do walks to the next
// peer; only when every peer says so does ErrNoBackend come back.
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

// Enqueue records a verified push and returns its job id, for operators; webhook.go
// keeps it out of the response body.
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

// List hands each of a project's job ids to emit. A consumed stream cannot be
// retried on another peer without repeating ids, so once anything was emitted a
// failure loses its Unavailable code and stops the walk.
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

// resumable keeps err's connect code while nothing was handed onwards, and strips
// it once something was. %v, not %w: a wrapped Unavailable would still be retried.
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

// Follow streams one run's output from `from` to its end and returns the offset
// reached. The request carries the run and offset, so walking to the next peer
// resumes rather than repeats; only the writer answers, others say Unavailable. A
// walk ending Unavailable is a log still to come; an ended run stops at the leader
// with FailedPrecondition. A failing emit is the caller's and stops the walk.
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

// Artifact is the cid of what one resource of one deployment runs, the read a
// worker makes. NotFound is preserved: "nothing built yet" waits, "unreachable"
// retries.
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
