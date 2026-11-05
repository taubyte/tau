package builder

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"connectrpc.com/connect"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	builderv2 "github.com/taubyte/tau/pkg/proto/gen/tau/builder/v2"
	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
	"github.com/taubyte/tau/services/plm"
	"github.com/taubyte/tau/services/plm/clients"
	"github.com/taubyte/tau/services/plm/run"
)

// served is runs served on their own peer of a mocknet, and a builder client to them
// from another.
func served(t *testing.T, runs *Runs) *clients.Builder {
	t.Helper()
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	at, err := mn.GenPeer()
	require.NoError(t, err)
	from, err := mn.GenPeer()
	require.NoError(t, err)
	require.NoError(t, mn.LinkAll())
	listen, err := rpc.Listen(t.Context(), at, plm.BuilderProtocol, Handler(runs))
	require.NoError(t, err)
	t.Cleanup(func() { listen.Close() })
	c, err := clients.BuilderAt(from, at.ID())
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	return c
}

func TestABuilderServesItsRunsOverItsProtocol(t *testing.T) {
	store := &keeper{}
	c := served(t, newTestRuns(t, keeps{resource: "QmResource", content: strings.NewReader("the artifact")}, store, Logs{}))

	planned, err := c.Plan(t.Context(), work("plan", ""))
	require.NoError(t, err)
	require.Empty(t, planned.GetError())
	_, err = c.Seal(t.Context(), sealing("plan", plmv2.JobStatus_JobStatusSuccess))
	require.NoError(t, err)

	w := work("build", "prod")
	w.Prelude = "main feeds prod\n"
	built, err := c.Build(t.Context(), w)
	require.NoError(t, err)
	require.Len(t, built.GetStored(), 1)

	following, err := c.Follow(t.Context(), &plmv2.FollowRequest{JobId: "build", Attempt: 1})
	require.NoError(t, err)
	first, err := following.Receive()
	require.NoError(t, err)
	require.Equal(t, plmv2.JobStatus_JobStatusClaimed, first.GetStatus(), "the first event was read ahead, and is the first handed over")
	said, err := following.Receive()
	require.NoError(t, err)
	require.Equal(t, "main feeds prod", said.GetMessage())

	require.NoError(t, c.Cancel(t.Context(), &builderv2.Run{JobId: "build", Attempt: 1}))
	sealed, err := c.Seal(t.Context(), sealing("build", plmv2.JobStatus_JobStatusSuccess))
	require.NoError(t, err)
	require.NotEmpty(t, sealed.GetLog())
	last, err := following.Receive()
	require.NoError(t, err)
	require.Equal(t, plmv2.JobStatus_JobStatusSuccess, last.GetStatus())
	_, err = following.Receive()
	require.ErrorIs(t, err, io.EOF)
	_, err = following.Receive()
	require.ErrorIs(t, err, io.EOF, "an ended follow stays ended")
	require.NoError(t, following.Close())
}

func TestWhatABuilderRefusesComesBackWithItsCode(t *testing.T) {
	build := newWaits()
	c := served(t, newTestRuns(t, build, nil, Logs{}))

	_, err := c.Follow(t.Context(), &plmv2.FollowRequest{JobId: "nothing", Attempt: 1})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "a run this builder holds no log of")
	_, err = c.Seal(t.Context(), sealing("nothing", plmv2.JobStatus_JobStatusSuccess))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	done := make(chan error, 1)
	go func() {
		_, err := c.Build(context.Background(), work("build", "prod"))
		done <- err
	}()
	<-build.started
	_, err = c.Plan(t.Context(), work("plan", ""))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err), "one run at a time")
	_, err = c.Build(t.Context(), work("again", "prod"))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	close(build.release)
	require.NoError(t, <-done)

	// A follow of an empty snapshot ends before its first event.
	snapshot, err := c.Follow(t.Context(), &plmv2.FollowRequest{JobId: "build", Attempt: 1, Snapshot: true, Offset: logSize(t, c, "build")})
	require.NoError(t, err)
	_, err = snapshot.Receive()
	require.ErrorIs(t, err, io.EOF)
	snapshot.Close()
}

// logSize is how far the run's log goes, read off its last event.
func logSize(t *testing.T, c *clients.Builder, job string) int64 {
	t.Helper()
	snapshot, err := c.Follow(t.Context(), &plmv2.FollowRequest{JobId: job, Attempt: 1, Snapshot: true})
	require.NoError(t, err)
	defer snapshot.Close()
	var at int64
	for {
		ev, err := snapshot.Receive()
		if errors.Is(err, io.EOF) {
			return at
		}
		require.NoError(t, err)
		at = ev.GetOffset()
	}
}

func TestAFollowerThatLeavesLetsGoOfTheLog(t *testing.T) {
	build := &speaks{lines: make(chan string), started: make(chan struct{})}
	c := served(t, newTestRuns(t, build, nil, Logs{}))
	done := make(chan error, 1)
	go func() {
		_, err := c.Build(context.Background(), work("build", "prod"))
		done <- err
	}()
	<-build.started

	ctx, cancel := context.WithCancel(t.Context())
	following, err := c.Follow(ctx, &plmv2.FollowRequest{JobId: "build", Attempt: 1})
	require.NoError(t, err)
	cancel()
	for {
		if _, err := following.Receive(); err != nil {
			break
		}
	}
	close(build.lines)
	require.NoError(t, <-done)
}

func TestABuilderRefusesWhatItCannotBeStartedWith(t *testing.T) {
	r := New()
	require.Error(t, r.Start(t.Context(), lone(t, map[string]any{"logs": map[string]any{"maxBytes": -1}})))
	require.NoError(t, r.Close())
}

func TestABuilderStartsServingAndClosesOnce(t *testing.T) {
	r := New()
	h := lone(t, map[string]any{"domains": map[string]any{"trusted": true}, "logs": map[string]any{"dir": t.TempDir()}})
	require.NoError(t, r.Start(t.Context(), h))
	require.ErrorContains(t, r.Start(t.Context(), h), "already started")

	taken := New()
	require.Error(t, taken.Start(t.Context(), h), "the protocol is the first builder's on this peer until it lets go")
	require.NoError(t, taken.Close())
	require.NoError(t, r.Close())
}

func TestWorkCrossesTheWireWhole(t *testing.T) {
	w := run.Work{JobID: "j", RepositoryKey: "github.com/acme/app", Ref: "refs/heads/main", CommitSHA: "c",
		Before: "b", ProjectID: "QmP", Attempt: 3, Environment: "prod", Prelude: "said"}
	require.Equal(t, w, run.WorkOf(w.Proto()))
}
