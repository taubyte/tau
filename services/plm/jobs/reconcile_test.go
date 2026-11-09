package jobs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
	"github.com/taubyte/tau/services/plm/run"
)

func TestReconcileReQueuesARecordWhosePushWasLost(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	// The state a crash between the record's entry and the queue's leaves.
	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.work.Ack(d))
	ready, _ := depthOf(t, h.work)
	require.Equal(t, 0, ready)

	// Not yet: the age gate is what keeps this pass from issuing a barrier per
	// open job on every run.
	require.NoError(t, h.reconcile())
	ready, _ = depthOf(t, h.work)
	require.Equal(t, 0, ready)

	h.clock.advance(h.cfg.Queue.VisibilityTimeout + h.cfg.BuildTimeout + time.Second)
	require.NoError(t, h.reconcile())

	ready, _ = depthOf(t, h.work)
	require.Equal(t, 1, ready)
	require.Equal(t, plmv2.JobStatus_JobStatusOpen, h.status(t, id))
}

func TestReconcileCannotDisturbAClaimedJob(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	d, err := h.work.Claim()
	require.NoError(t, err)

	h.clock.advance(h.cfg.Queue.VisibilityTimeout + h.cfg.BuildTimeout + time.Second)
	require.NoError(t, h.reconcile())

	ready, claimed := depthOf(t, h.work)
	require.Equal(t, 0, ready)
	require.Equal(t, 1, claimed, "the claim must survive a reconcile")

	// And the delivery is still the live one.
	require.NoError(t, h.work.Ack(d))
	require.Equal(t, plmv2.JobStatus_JobStatusOpen, h.status(t, id))
}

func TestReconcileDropsStaleOpenRows(t *testing.T) {
	h := newHarness(t)
	finished := h.planned(t, testRef, testCommit)
	require.NoError(t, h.finishNow(t, finished, plmv2.JobStatus_JobStatusSuccess, ""))
	// finish already removed the row; put it back to stand for an ending whose read
	// had not seen the opening.
	h.plant(t, testRepo, finished)

	h.plant(t, testRepo, unknownJobID)

	require.Len(t, h.records.keys(t, openRoot()), 2)
	require.NoError(t, h.reconcile())
	require.Empty(t, h.records.keys(t, openRoot()))
}

func TestReconcileIsLeaderOnly(t *testing.T) {
	h := newHarness(t)
	h.planned(t, testRef, testCommit)
	h.store.stepDown()

	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(h.reconcile()))
}

func TestReconcileReportsWhatItCannotRead(t *testing.T) {
	t.Run("an open row that does not decode", func(t *testing.T) {
		h := newHarness(t)
		h.records.put(t, openRows(testRepo)+"!!!", nil)

		require.Equal(t, connect.CodeInternal, connect.CodeOf(h.reconcile()))
	})

	t.Run("an event that does not decode", func(t *testing.T) {
		h := newHarness(t)
		id := h.planned(t, testRef, testCommit)
		h.records.garble(t, id)

		require.Equal(t, connect.CodeInternal, connect.CodeOf(h.reconcile()))
	})

	t.Run("a store that refuses the repair", func(t *testing.T) {
		h := newHarness(t)
		h.plant(t, testRepo, unknownJobID)
		h.records.failBatches(errors.New("the store is full"))

		require.Error(t, h.reconcile())
	})

	t.Run("a store that refuses the re-queue", func(t *testing.T) {
		h := newHarness(t)
		id := h.planned(t, testRef, testCommit)
		d, err := h.work.Claim()
		require.NoError(t, err)
		require.NoError(t, h.work.Ack(d))

		h.clock.advance(h.cfg.Queue.VisibilityTimeout + h.cfg.BuildTimeout + time.Second)
		h.store.failBatches(errors.New("log is full"))

		require.ErrorContains(t, h.reconcile(), id)
	})
}

func TestTheStartedLoopReconciles(t *testing.T) {
	h := newHarnessCfg(t, func(c *Config) {
		c.ClaimInterval = time.Hour // only reconcile may fire
		c.ReconcileEvery = time.Millisecond
	})
	id := h.planned(t, testRef, testCommit)
	// NOTHING CLAIMS once the loop runs, or the job reconcile re-queues is gone again.
	h.builders = nil

	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.work.Ack(d))
	h.clock.advance(h.cfg.Queue.VisibilityTimeout + h.cfg.BuildTimeout + time.Second)

	require.NoError(t, h.serve(t.Context()))
	require.Eventually(t, func() bool {
		ready, _ := depthOf(t, h.work)
		return ready == 1
	}, 5*time.Second, time.Millisecond, "the reconcile ticker never re-queued job %s", id)
}

// elsewhere appends one event of a job as another node's writer, with the rows
// that went in its delta, straight into the store: a delta replicated here.
func (h *harness) elsewhere(t testing.TB, id string, ev *plmv2.JobEvent, rows ...func(at int64, writer string) string) {
	t.Helper()
	ev.At, ev.Writer = h.journal.at(), "elsewhere"
	raw, err := ev.MarshalVT()
	require.NoError(t, err)
	h.records.put(t, eventKey(id, ev.At, ev.Writer), raw)
	for _, row := range rows {
		h.records.put(t, row(ev.At, ev.Writer), nil)
	}
}

// A REOPENING THIS REPLICA HAD NOT SEEN KEEPS ITS ROW. Another node's retry
// arrives between mend's read of the job and its listing of the job's rows: mend
// decided on an ended job, and the row it now lists is beside an event that read
// never saw.
func TestMendDoesNotDropTheRowOfAReopeningItHadNotRead(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.work.Ack(d))
	require.NoError(t, h.finishNow(t, id, plmv2.JobStatus_JobStatusFailed, "boom"))
	h.plant(t, testRepo, id)
	stale := h.records.keys(t, openRowsOf(testRepo, id))
	require.Len(t, stale, 1)

	h.records.onList(eventPrefix(id), func() {
		h.elsewhere(t, id, &plmv2.JobEvent{Kind: &plmv2.JobEvent_Retried{Retried: &plmv2.JobRetried{}}},
			func(at int64, writer string) string { return openRow(testRepo, id, at, writer) })
	})
	require.NoError(t, h.reconcile())

	rows := h.records.keys(t, openRowsOf(testRepo, id))
	t.Logf("status=%s rows=%v", h.status(t, id), rows)
	require.Equal(t, plmv2.JobStatus_JobStatusOpen, h.status(t, id), "the retry reopened the job")
	require.Len(t, rows, 1, "the reopening's row survives")
	require.NotContains(t, rows, stale[0], "and the row of the opening the read saw end is gone")
}

// A ROW WITH NO EVENTS IS LEFT UNTIL IT IS OLD: its delta may have arrived ahead
// of the created one it follows.
func TestMendLeavesAYoungRowWithNoEventsBehindIt(t *testing.T) {
	h := newHarness(t)
	h.records.put(t, openRow(testRepo, unknownJobID, h.journal.at(), "elsewhere"), nil)

	require.NoError(t, h.reconcile())
	require.Len(t, h.records.keys(t, openRoot()), 1)

	h.clock.advance(h.cfg.Queue.VisibilityTimeout + h.cfg.BuildTimeout + time.Second)
	require.NoError(t, h.reconcile())
	require.Empty(t, h.records.keys(t, openRoot()))
}

// pastRetention ages the harness past the job retention.
func (h *harness) pastRetention() {
	h.clock.advance(h.cfg.JobRetention + time.Hour)
}

func TestAJobIsErasedOnceItIsPastRetention(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	require.True(t, h.mustStep(t))
	require.Equal(t, plmv2.JobStatus_JobStatusSuccess, h.status(t, id))
	plan := h.rows(t)
	require.NotEmpty(t, plan)

	// NOT YET: a finished job is kept for the retention.
	require.NoError(t, h.reconcile())
	require.NotEmpty(t, h.records.keys(t, eventPrefix(id)))

	h.pastRetention()
	require.NoError(t, h.reconcile())

	_, found, err := h.readRecord(id)
	require.NoError(t, err)
	require.False(t, found, "an expired job is gone")
	for _, key := range h.rows(t) {
		require.NotContains(t, key, seg(id), "and so is every row that named it")
	}
	require.Empty(t, h.lastCommit(testRepo, testRef, testEnvironment),
		"its anchor went with it, so the next build is a full one")
}

// WHAT A DEPLOYMENT RUNS IS KEPT HOWEVER OLD: it is resolved through the job that
// built it. Once a newer build supersedes it, it goes.
func TestAJobWhoseBuildStillRunsIsKept(t *testing.T) {
	h := newHarness(t)
	build := func(ref, content string) string {
		t.Helper()
		id := h.planned(t, ref, testCommit)
		h.builder.run = func(ctx context.Context, _ run.Work, out run.Output) error {
			return out.Asset(ctx, "website/home", strings.NewReader(content))
		}
		require.True(t, h.mustStep(t))
		require.Equal(t, plmv2.JobStatus_JobStatusSuccess, h.status(t, id))
		return id
	}
	project := "project-of-" + testRepo

	running := build(testRef, "the first build")
	h.pastRetention()
	require.NoError(t, h.reconcile())
	require.Equal(t, plmv2.JobStatus_JobStatusSuccess, h.status(t, running), "it is what runs")
	cid, err := h.Built(project, testEnvironment, "website/home")
	require.NoError(t, err)
	require.Equal(t, assetCID([]byte("the first build")), cid)

	newer := build("refs/heads/next", "the second build")
	require.NoError(t, h.reconcile())
	_, found, err := h.readRecord(running)
	require.NoError(t, err)
	require.False(t, found, "superseded, it expires")
	require.Equal(t, plmv2.JobStatus_JobStatusSuccess, h.status(t, newer), "and the young one stays")
	cid, err = h.Built(project, testEnvironment, "website/home")
	require.NoError(t, err)
	require.Equal(t, assetCID([]byte("the second build")), cid)
}

func TestARetentionRowItsJobOutlivedIsDropped(t *testing.T) {
	h := newHarness(t)
	both := h.plannedEach(t, testRef, otherRef)
	gone, reopened := both[0], both[1]
	require.NoError(t, h.finishNow(t, gone, plmv2.JobStatus_JobStatusFailed, "boom"))
	h.records.erase(t, gone)

	require.NoError(t, h.finishNow(t, reopened, plmv2.JobStatus_JobStatusFailed, "boom"))
	stale := h.terminal(t, reopened)
	require.Len(t, stale, 1)
	for range both {
		d, err := h.work.Claim()
		require.NoError(t, err)
		require.NoError(t, h.work.Ack(d))
	}
	require.NoError(t, h.retry(reopened))
	h.records.put(t, stale[0], nil)

	h.pastRetention()
	require.NoError(t, h.reconcile())

	require.Empty(t, h.terminal(t, gone), "a row whose job is gone goes")
	require.Empty(t, h.terminal(t, reopened), "and so does one whose job is open again")
	require.Equal(t, plmv2.JobStatus_JobStatusOpen, h.status(t, reopened), "which is not erased")
}

func TestExpiryReportsWhatItCannotRead(t *testing.T) {
	t.Run("a retention row that does not decode", func(t *testing.T) {
		h := newHarness(t)
		h.records.put(t, terminalRows()+"!!!", nil)
		require.Equal(t, connect.CodeInternal, connect.CodeOf(h.reconcile()))
	})

	t.Run("an event that does not decode", func(t *testing.T) {
		h := newHarness(t)
		id := h.planned(t, testRef, testCommit)
		require.NoError(t, h.finishNow(t, id, plmv2.JobStatus_JobStatusFailed, "boom"))
		h.records.garble(t, id)
		h.pastRetention()
		require.Equal(t, connect.CodeInternal, connect.CodeOf(h.reconcile()))
	})

	t.Run("a store that refuses the erase", func(t *testing.T) {
		h := newHarness(t)
		id := h.planned(t, testRef, testCommit)
		require.NoError(t, h.finishNow(t, id, plmv2.JobStatus_JobStatusFailed, "boom"))
		h.pastRetention()
		h.records.failBatches(errors.New("the store is full"))
		require.ErrorContains(t, h.reconcile(), "erasing job ", "the plan or its environment job, whichever is first")
		h.records.failBatches(nil)
		require.NotEmpty(t, h.records.keys(t, eventPrefix(id)), "and nothing was erased")
	})
}
