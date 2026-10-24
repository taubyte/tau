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

// wireHarness is a running Service on one mocknet host, reached from a second
// over the real transport: connect-go, over go-libp2p-gostream, over mocknet.
// No port is bound.
//
// This is the ONLY way any test in this package gets a caller identity a
// handler was not separately told about — rpc.RemotePeer reads a context value
// under a key private to package rpc, so nothing outside it can fabricate one —
// and the only way to exercise the streamed List, since connect.ServerStream
// has no exported constructor.
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
	s, err := New(Config{}, append([]Option{WithHost(server), WithStore(store), WithRepositories(repos)}, opts...)...)
	require.NoError(t, err)
	require.NoError(t, s.Start(t.Context()))
	t.Cleanup(func() { s.Close() })

	c, err := rpc.NewClient(callerHost, server.ID(), build.Protocol)
	require.NoError(t, err)
	t.Cleanup(c.Close)

	return &wireHarness{
		harness: &harness{
			Service: s, store: store, repos: repos, runner: runner, clock: clock, host: server,
		},
		authorizer: authorizer,
		caller:     callerHost,
		Queue:      buildv2connect.NewQueueServiceClient(c.HTTP(), c.URL()),
		Jobs:       buildv2connect.NewJobServiceClient(c.HTTP(), c.URL()),
	}
}

// One end-to-end over the real transport, covering all five RPCs — including
// the streamed List, which cannot be proven any other way — and asserting that
// the identity the handler authorized on is the CALLING host's peer id, taken
// from the authenticated stream and not from anything the caller sent.
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

// One deny per guarded RPC, over the wire, so the code the caller sees is the
// one the authorizer produced rather than one connect invented.
func TestADeniedCallerIsRefusedOnEveryRPC(t *testing.T) {
	for _, tc := range []struct {
		act  Action
		call func(*wireHarness, string) error
	}{
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
			id := h.push(t, testRef, testCommit)

			h.authorizer.deny, h.authorizer.denyOn = tc.act, true
			require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(tc.call(h, id)))
		})
	}

	t.Run("list", func(t *testing.T) {
		h := newWireHarness(t)
		h.push(t, testRef, testCommit)
		h.authorizer.deny, h.authorizer.denyOn = ActRead, true

		stream, err := h.Jobs.List(t.Context(), connect.NewRequest(&buildv2.ProjectRef{Id: "p"}))
		require.NoError(t, err)
		require.False(t, stream.Receive())
		require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(stream.Err()))
	})
}

// A follower answers "ask somebody else" over the wire too, which is the code
// clients.Pool.Do walks past to the next peer.
func TestAFollowerAnswersUnavailableOverTheWire(t *testing.T) {
	h := newWireHarness(t)
	h.store.stepDown()

	_, err := h.Queue.Enqueue(t.Context(), connect.NewRequest(&buildv2.PushEvent{
		RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
	}))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
}

// The connect adapters pass a domain method's error through unchanged, so the
// caller sees the code this service decided rather than one connect invented
// for an unexpected return.
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

// THE SNAPSHOT CROSSES THE WIRE: asked over the RPC a remote caller uses, it
// ends at what the running build has written, while a follow on the same RPC
// stays open for the next line and ends with the build.
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

	id := h.push(t, testRef, testCommit)
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the claim loop never started the build")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	snapshot, err := h.Jobs.Follow(ctx, connect.NewRequest(&buildv2.FollowRequest{JobId: id, Snapshot: true}))
	require.NoError(t, err)
	var soFar []byte
	for snapshot.Receive() {
		soFar = append(soFar, snapshot.Msg().GetData()...)
	}
	require.NoError(t, snapshot.Err(), "a snapshot that waited for the build would have run into the deadline")
	require.Equal(t, "cloning\n", string(soFar))

	follow, err := h.Jobs.Follow(ctx, connect.NewRequest(&buildv2.FollowRequest{JobId: id}))
	require.NoError(t, err)
	defer follow.Close()
	require.True(t, follow.Receive(), "%v", follow.Err())
	require.Equal(t, "cloning\n", string(follow.Msg().GetData()))

	lines <- "compiling\n"
	require.True(t, follow.Receive(), "%v", follow.Err())
	require.Equal(t, "compiling\n", string(follow.Msg().GetData()))

	finish()
	require.False(t, follow.Receive())
	require.NoError(t, follow.Err(), "the build ending is the end of the follow, not a failure of it")
}
