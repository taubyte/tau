package backend

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	buildv1 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v1"
	"github.com/taubyte/tau/pkg/proto/gen/tau/build/v1/buildv1connect"
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
	Queue      buildv1connect.QueueServiceClient
	Jobs       buildv1connect.JobServiceClient
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
	s, err := New(server, store, repos, Config{}, opts...)
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
		Queue:      buildv1connect.NewQueueServiceClient(c.HTTP(), c.URL()),
		Jobs:       buildv1connect.NewJobServiceClient(c.HTTP(), c.URL()),
	}
}

// One end-to-end over the real transport, covering all five RPCs — including
// the streamed List, which cannot be proven any other way — and asserting that
// the identity the handler authorized on is the CALLING host's peer id, taken
// from the authenticated stream and not from anything the caller sent.
func TestEveryRPCOverTheWireAuthorizesOnTheCallingPeer(t *testing.T) {
	h := newWireHarness(t)

	ref, err := h.Queue.Enqueue(t.Context(), connect.NewRequest(&buildv1.PushEvent{
		RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
	}))
	require.NoError(t, err)
	id := ref.Msg.GetId()
	require.NoError(t, requireJobID(id))

	job, err := h.Jobs.Get(t.Context(), connect.NewRequest(&buildv1.JobRef{Id: id}))
	require.NoError(t, err)
	require.Equal(t, testRepo, job.Msg.GetRepositoryKey())

	stream, err := h.Jobs.List(t.Context(), connect.NewRequest(&buildv1.ProjectRef{Id: "project-of-" + testRepo}))
	require.NoError(t, err)
	var listed []string
	for stream.Receive() {
		listed = append(listed, stream.Msg().GetId())
	}
	require.NoError(t, stream.Err())
	require.NoError(t, stream.Close())
	require.Equal(t, []string{id}, listed)

	_, err = h.Jobs.Cancel(t.Context(), connect.NewRequest(&buildv1.JobRef{Id: id}))
	require.NoError(t, err)
	require.Equal(t, buildv1.JobStatus_JobStatusCancelled, h.status(t, id))

	_, err = h.Jobs.Retry(t.Context(), connect.NewRequest(&buildv1.JobRef{Id: id}))
	require.NoError(t, err)
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id))

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
			_, err := h.Queue.Enqueue(context.Background(), connect.NewRequest(&buildv1.PushEvent{
				RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
			}))
			return err
		}},
		{ActRead, func(h *wireHarness, id string) error {
			_, err := h.Jobs.Get(context.Background(), connect.NewRequest(&buildv1.JobRef{Id: id}))
			return err
		}},
		{ActCancel, func(h *wireHarness, id string) error {
			_, err := h.Jobs.Cancel(context.Background(), connect.NewRequest(&buildv1.JobRef{Id: id}))
			return err
		}},
		{ActRetry, func(h *wireHarness, id string) error {
			_, err := h.Jobs.Retry(context.Background(), connect.NewRequest(&buildv1.JobRef{Id: id}))
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

		stream, err := h.Jobs.List(t.Context(), connect.NewRequest(&buildv1.ProjectRef{Id: "p"}))
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

	_, err := h.Queue.Enqueue(t.Context(), connect.NewRequest(&buildv1.PushEvent{
		RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
	}))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
}

// The connect adapters pass a domain method's error through unchanged, so the
// caller sees the code this service decided rather than one connect invented
// for an unexpected return.
func TestAnUnknownJobIsNotFoundOverEveryJobRPC(t *testing.T) {
	h := newWireHarness(t)
	ref := connect.NewRequest(&buildv1.JobRef{Id: unknownJobID})

	_, err := h.Jobs.Get(t.Context(), ref)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	_, err = h.Jobs.Cancel(t.Context(), connect.NewRequest(&buildv1.JobRef{Id: unknownJobID}))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	_, err = h.Jobs.Retry(t.Context(), connect.NewRequest(&buildv1.JobRef{Id: unknownJobID}))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}
