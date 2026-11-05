package jobs

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/peer"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/services/build/run"
)

func TestADirectPushRunsToItsEndInTheCallerAwaitingIt(t *testing.T) {
	h := newHarness(t)
	h.runner.run = func(ctx context.Context, _ run.Work, out run.Output) error {
		return out.Asset(ctx, "QmResource", strings.NewReader("wasm!"))
	}
	d := &Direct{Local: Local{s: h.server}}

	plan, err := d.Await(t.Context(), h.push(t, testRef, testCommit))
	require.NoError(t, err)
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, plan.GetStatus())
	require.Len(t, plan.GetChildren(), 1, "the plan queued the environment it feeds")

	built, err := d.Await(t.Context(), plan.GetChildren()[0])
	require.NoError(t, err)
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, built.GetStatus(), "and it ran before the plan's await returned")
	require.Len(t, h.runner.seen(), 1)
	cid, err := h.Built("project-of-"+testRepo, testEnvironment, "QmResource")
	require.NoError(t, err)
	require.Equal(t, assetCID([]byte("wasm!")), cid, "what it built is live, as a queued build's is")
}

func TestADirectPlanThatFailsEndsTheJob(t *testing.T) {
	h := newHarness(t)
	h.runner.plan = func(context.Context, run.Work, io.Writer) ([]string, error) { return nil, errors.New("no config") }
	d := &Direct{Local: Local{s: h.server}}

	plan, err := d.Await(t.Context(), h.push(t, testRef, testCommit))
	require.NoError(t, err)
	require.Equal(t, buildv2.JobStatus_JobStatusFailed, plan.GetStatus())
	require.Contains(t, plan.GetReason(), "no config")
}

func TestADirectJobNothingCanRunIsRefusedByName(t *testing.T) {
	h := newHarness(t)
	d := &Direct{Local: Local{s: h.server}}
	id := h.push(t, testRef, testCommit)
	_, err := h.work.Claim()
	require.NoError(t, err, "claimed by somebody else, so nothing is due here")

	_, err = d.Await(t.Context(), id)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	require.ErrorContains(t, err, "nothing is due")

	_, err = d.Await(t.Context(), strings.Repeat("0", 64))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestADirectStepThatFailsIsTheAwaitsError(t *testing.T) {
	h := newHarness(t)
	d := &Direct{Local: Local{s: h.server}}
	id := h.push(t, testRef, testCommit)
	h.store.stepDown()
	_, err := d.Await(t.Context(), id)
	require.Error(t, err)
}

func TestAwaitingAQueuedJobWaitsForTheRunThatEndsIt(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	type awaited struct {
		job *buildv2.Job
		err error
	}
	ended := make(chan awaited, 1)
	go func() {
		job, err := Local{s: h.server}.Await(t.Context(), id)
		ended <- awaited{job, err}
	}()
	for {
		ran, err := h.step(t.Context())
		require.NoError(t, err)
		if !ran {
			break
		}
	}
	got := <-ended
	require.NoError(t, got.err)
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, got.job.GetStatus())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := Local{s: h.server}.Await(ctx, h.push(t, testRef, strings.Repeat("1", 40)))
	require.ErrorIs(t, err, context.Canceled, "a job still queued is waited for until the caller stops")
	_, err = Local{s: h.server}.Await(t.Context(), "not a job")
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestJobsAloneKeepTheirRecordsWhereABuildNodeDoes(t *testing.T) {
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	d, err := Alone(&testHost{Node: p, root: t.TempDir()}, &fakeRunner{})
	require.NoError(t, err)
	_, err = d.Get(t.Context(), strings.Repeat("0", 64))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "an empty database, read without a queue to lead")
	require.NoError(t, d.Close())
	require.NoError(t, d.Close(), "closing twice is closing once")

	_, err = Alone(&testHost{Node: p, root: t.TempDir()}, nil)
	require.ErrorContains(t, err, "runner must not be nil")
}
