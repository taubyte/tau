package jobs

import (
	"context"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2/plmv2connect"
)

func TestAllowAnyPeerIsTheDefaultAndAllowsAnyone(t *testing.T) {
	h := newHarness(t)
	require.IsType(t, allowAnyPeer{}, h.authz)
	require.NoError(t, h.authz.Allow(t.Context(), peer.ID("anyone"), ActEnqueue, "x"))
}

func TestAllowPeersNarrowsToTheListedOnes(t *testing.T) {
	a := AllowPeers(peer.ID("build-1"), peer.ID("build-2"))

	require.NoError(t, a.Allow(t.Context(), peer.ID("build-1"), ActEnqueue, "x"))
	require.NoError(t, a.Allow(t.Context(), peer.ID("build-2"), ActRead, "x"))

	err := a.Allow(t.Context(), peer.ID("stranger"), ActEnqueue, "x")
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	require.ErrorContains(t, err, "enqueue")

	require.Error(t, AllowPeers().Allow(t.Context(), peer.ID("anyone"), ActRead, "x"))
}

func TestACallerWithNoStreamIsUnauthenticated(t *testing.T) {
	_, err := caller(context.Background())
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	require.ErrorIs(t, err, rpc.ErrNoRemotePeer)
}

func TestTheseHandlersCannotBeServedOnAnHTTPIngress(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	srv := httptest.NewServer(h.mount())
	t.Cleanup(srv.Close)

	queueClient := plmv2connect.NewQueueServiceClient(srv.Client(), srv.URL)
	jobClient := plmv2connect.NewJobServiceClient(srv.Client(), srv.URL)

	_, err := queueClient.Enqueue(t.Context(), connect.NewRequest(&plmv2.PushEvent{
		RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
	}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	_, err = jobClient.Get(t.Context(), connect.NewRequest(&plmv2.JobRef{Id: id}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	_, err = jobClient.Cancel(t.Context(), connect.NewRequest(&plmv2.JobRef{Id: id}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	_, err = jobClient.Retry(t.Context(), connect.NewRequest(&plmv2.JobRef{Id: id}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	stream, err := jobClient.List(t.Context(), connect.NewRequest(&plmv2.ProjectRef{Id: "p"}))
	require.NoError(t, err)
	require.False(t, stream.Receive())
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(stream.Err()))

	require.Equal(t, plmv2.JobStatus_JobStatusOpen, h.status(t, id),
		"nothing reached over an ingress may have changed anything")
}
