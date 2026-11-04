package backend

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/build/v2/buildv2connect"
	"github.com/taubyte/tau/services/build"
)

// recordingAuthorizer captures what the handler resolved the caller to, which
// is the fact this file exists to prove.
type recordingAuthorizer struct {
	seen   []peer.ID
	acts   []Action
	subs   []string
	deny   Action
	denyOn bool
}

func (r *recordingAuthorizer) Allow(_ context.Context, caller peer.ID, act Action, subject string) error {
	r.seen = append(r.seen, caller)
	r.acts = append(r.acts, act)
	r.subs = append(r.subs, subject)
	if r.denyOn && act == r.deny {
		return errPermissionDenied("peer %s may not %s", caller, act)
	}
	return nil
}

// wireHarness is a running Service on one mocknet host reached from another over
// the real transport, the only way to get a real caller identity and to exercise
// the streamed List.
type wireHarness struct {
	*harness
	authorizer *recordingAuthorizer
	caller     host.Host
	Queue      buildv2connect.QueueServiceClient
	Jobs       buildv2connect.JobServiceClient
}

func newWireHarness(t *testing.T, opts ...Option) *wireHarness {
	t.Helper()

	authorizer := &recordingAuthorizer{}
	mn := newMocknet(t)
	server := newHost(t, mn)
	callerHost := newHost(t, mn)

	store := newStore()
	repos := newRepositories(testRepo)
	runner := &fakeRunner{}
	clock := newClock()

	opts = append([]Option{WithClock(clock.now), WithRunner(runner), WithAuthorizer(authorizer)}, opts...)
	records := newRecords(t)
	s, err := New(Config{}, append([]Option{WithHost(server), WithStore(store), WithRecords(records), WithRepositories(repos)}, opts...)...)
	require.NoError(t, err)
	require.NoError(t, s.Start(t.Context()))
	t.Cleanup(func() { s.Close() })

	c, err := rpc.NewClient(callerHost, server.ID(), build.Protocol)
	require.NoError(t, err)
	t.Cleanup(c.Close)

	return &wireHarness{
		harness: &harness{
			Service: s, store: store, records: records, repos: repos, runner: runner, clock: clock, host: server,
		},
		authorizer: authorizer,
		caller:     callerHost,
		Queue:      buildv2connect.NewQueueServiceClient(c.HTTP(), c.URL()),
		Jobs:       buildv2connect.NewJobServiceClient(c.HTTP(), c.URL()),
	}
}

func TestEveryRPCOverTheWireAuthorizesOnTheCallingPeer(t *testing.T) {
	h := newWireHarness(t)

	ref, err := h.Queue.Enqueue(t.Context(), connect.NewRequest(&buildv2.PushEvent{
		RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
	}))
	require.NoError(t, err)
	id := ref.Msg.GetId()
	require.NoError(t, requireJobID(id))

	job, err := h.Jobs.Get(t.Context(), connect.NewRequest(&buildv2.JobRef{Id: id}))
	require.NoError(t, err)
	require.Equal(t, testRepo, job.Msg.GetRepositoryKey())

	stream, err := h.Jobs.List(t.Context(), connect.NewRequest(&buildv2.ProjectRef{Id: "project-of-" + testRepo}))
	require.NoError(t, err)
	var listed []string
	for stream.Receive() {
		listed = append(listed, stream.Msg().GetId())
	}
	require.NoError(t, stream.Err())
	require.NoError(t, stream.Close())
	require.Equal(t, []string{id}, listed)

	_, err = h.Jobs.Cancel(t.Context(), connect.NewRequest(&buildv2.JobRef{Id: id}))
	require.NoError(t, err)
	require.Equal(t, buildv2.JobStatus_JobStatusCancelled, h.status(t, id))

	_, err = h.Jobs.Retry(t.Context(), connect.NewRequest(&buildv2.JobRef{Id: id}))
	require.NoError(t, err)
	require.Equal(t, buildv2.JobStatus_JobStatusOpen, h.status(t, id))

	require.Equal(t, []Action{ActEnqueue, ActRead, ActRead, ActCancel, ActRetry}, h.authorizer.acts)
	require.Equal(t, []string{testRepo, id, "project-of-" + testRepo, id, id}, h.authorizer.subs)
	for _, seen := range h.authorizer.seen {
		require.Equal(t, h.caller.ID(), seen,
			"the handler must authorize on the calling host's own peer id")
	}
	require.NotEqual(t, h.host.ID(), h.caller.ID())
}

func TestADeniedCallerIsRefusedOnEveryRPC(t *testing.T) {
	type testCase struct {
		act  Action
		call func(*wireHarness, string) error
	}
	for _, tc := range []testCase{
		{ActEnqueue, func(h *wireHarness, _ string) error {
			_, err := h.Queue.Enqueue(context.Background(), connect.NewRequest(&buildv2.PushEvent{
				RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
			}))
			return err
		}},
		{ActRead, func(h *wireHarness, id string) error {
			_, err := h.Jobs.Get(context.Background(), connect.NewRequest(&buildv2.JobRef{Id: id}))
			return err
		}},
		{ActCancel, func(h *wireHarness, id string) error {
			_, err := h.Jobs.Cancel(context.Background(), connect.NewRequest(&buildv2.JobRef{Id: id}))
			return err
		}},
		{ActRetry, func(h *wireHarness, id string) error {
			_, err := h.Jobs.Retry(context.Background(), connect.NewRequest(&buildv2.JobRef{Id: id}))
			return err
		}},
	} {
		t.Run(tc.act.String(), func(t *testing.T) {
			h := newWireHarness(t)
			id := h.planned(t, testRef, testCommit)

			h.authorizer.deny, h.authorizer.denyOn = tc.act, true
			require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(tc.call(h, id)))
		})
	}

	t.Run("list", func(t *testing.T) {
		h := newWireHarness(t)
		h.planned(t, testRef, testCommit)
		h.authorizer.deny, h.authorizer.denyOn = ActRead, true

		stream, err := h.Jobs.List(t.Context(), connect.NewRequest(&buildv2.ProjectRef{Id: "p"}))
		require.NoError(t, err)
		require.False(t, stream.Receive())
		require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(stream.Err()))
	})
}

func TestAFollowerAnswersUnavailableOverTheWire(t *testing.T) {
	h := newWireHarness(t)
	h.store.stepDown()

	_, err := h.Queue.Enqueue(t.Context(), connect.NewRequest(&buildv2.PushEvent{
		RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
	}))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
}

func TestAnUnknownJobIsNotFoundOverEveryJobRPC(t *testing.T) {
	h := newWireHarness(t)
	ref := connect.NewRequest(&buildv2.JobRef{Id: unknownJobID})

	_, err := h.Jobs.Get(t.Context(), ref)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	_, err = h.Jobs.Cancel(t.Context(), connect.NewRequest(&buildv2.JobRef{Id: unknownJobID}))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	_, err = h.Jobs.Retry(t.Context(), connect.NewRequest(&buildv2.JobRef{Id: unknownJobID}))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// built asks over the wire what one resource of one deployment runs.
func (h *wireHarness) built(t *testing.T, project, environment, resource string) (string, error) {
	t.Helper()

	res, err := h.Jobs.Artifact(t.Context(), connect.NewRequest(&buildv2.ArtifactRequest{
		Project: project, Environment: environment, Resource: resource,
	}))
	if err != nil {
		return "", err
	}
	return res.Msg.GetCid(), nil
}

func TestASnapshotEndsOverTheWireWhileAFollowStaysOpen(t *testing.T) {
	h := newWireHarness(t)
	started, lines := make(chan struct{}), make(chan string)
	h.runner.mu.Lock()
	h.runner.run = func(ctx context.Context, _ Work, out Output) error {
		fmt.Fprint(out, "cloning\n")
		close(started)
		for {
			select {
			case line, more := <-lines:
				if !more {
					return nil
				}
				fmt.Fprint(out, line)
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	h.runner.mu.Unlock()
	var once sync.Once
	finish := func() { once.Do(func() { close(lines) }) }
	t.Cleanup(finish)

	id := h.planned(t, testRef, testCommit)
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the claim loop never started the build")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	snapshot, err := h.Jobs.Follow(ctx, connect.NewRequest(&buildv2.FollowRequest{JobId: id, Attempt: 1, Snapshot: true}))
	require.NoError(t, err)
	var soFar []*buildv2.Job_Event
	for snapshot.Receive() {
		soFar = append(soFar, snapshot.Msg())
	}
	require.NoError(t, snapshot.Err(), "a snapshot that waited for the build would have run into the deadline")
	require.Equal(t, "cloning\n", messagesOf(soFar))

	follow, err := h.Jobs.Follow(ctx, connect.NewRequest(&buildv2.FollowRequest{JobId: id, Attempt: 1}))
	require.NoError(t, err)
	defer follow.Close()
	next := func() *buildv2.Job_Event {
		t.Helper()
		require.True(t, follow.Receive(), "%v", follow.Err())
		return follow.Msg()
	}
	require.Equal(t, buildv2.JobStatus_JobStatusClaimed, next().GetStatus())
	require.Equal(t, "cloning", next().GetMessage())

	lines <- "compiling\n"
	require.Equal(t, "compiling", next().GetMessage())

	finish()
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, next().GetStatus(), "the last event is how the run left the job")
	require.False(t, follow.Receive())
	require.NoError(t, follow.Err(), "the build ending is the end of the follow, not a failure of it")
}
