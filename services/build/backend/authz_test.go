package backend

import (
	"context"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	buildv1 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v1"
	"github.com/taubyte/tau/pkg/proto/gen/tau/build/v1/buildv1connect"
)

func TestAllowAnyPeerIsTheDefaultAndAllowsAnyone(t *testing.T) {
	h := newHarness(t)
	require.IsType(t, allowAnyPeer{}, h.authz)
	require.NoError(t, h.authz.Allow(t.Context(), peer.ID("anyone"), ActEnqueue, "x"))
}

func TestAllowPeersNarrowsToTheListedOnes(t *testing.T) {
	a := AllowPeers(peer.ID("frontend-1"), peer.ID("frontend-2"))

	require.NoError(t, a.Allow(t.Context(), peer.ID("frontend-1"), ActEnqueue, "x"))
	require.NoError(t, a.Allow(t.Context(), peer.ID("frontend-2"), ActRead, "x"))

	err := a.Allow(t.Context(), peer.ID("stranger"), ActEnqueue, "x")
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	require.ErrorContains(t, err, "enqueue")

	require.Error(t, AllowPeers().Allow(t.Context(), peer.ID("anyone"), ActRead, "x"))
}

// A caller identity comes from the stream, so a context that did not arrive
// over one of p2p/rpc's listeners has none — and this service refuses rather
// than guessing.
func TestACallerWithNoStreamIsUnauthenticated(t *testing.T) {
	_, err := caller(context.Background())
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	require.ErrorIs(t, err, rpc.ErrNoRemotePeer)
}

// THE TRUST BOUNDARY, AS A TEST. Mount this service's own mux on a plain HTTP
// listener — which is what a frontend growing "just one route" onto the queue
// would be — and every RPC answers Unauthenticated, because rpc.RemotePeer
// finds no libp2p stream under the request. The handlers are structurally
// unmountable on an ingress, which is the one mistake the whole split is about.
func TestTheseHandlersCannotBeServedOnAnHTTPIngress(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	srv := httptest.NewServer(h.mount())
	t.Cleanup(srv.Close)

	queueClient := buildv1connect.NewQueueServiceClient(srv.Client(), srv.URL)
	jobClient := buildv1connect.NewJobServiceClient(srv.Client(), srv.URL)

	_, err := queueClient.Enqueue(t.Context(), connect.NewRequest(&buildv1.PushEvent{
		RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
	}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	_, err = jobClient.Get(t.Context(), connect.NewRequest(&buildv1.JobRef{Id: id}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	_, err = jobClient.Cancel(t.Context(), connect.NewRequest(&buildv1.JobRef{Id: id}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	_, err = jobClient.Retry(t.Context(), connect.NewRequest(&buildv1.JobRef{Id: id}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	stream, err := jobClient.List(t.Context(), connect.NewRequest(&buildv1.ProjectRef{Id: "p"}))
	require.NoError(t, err)
	require.False(t, stream.Receive())
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(stream.Err()))

	require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id),
		"nothing reached over an ingress may have changed anything")
}
