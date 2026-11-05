package jobs

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
	"github.com/taubyte/tau/services/plm/run"
)

const unknownJobID = "0000000000000000000000000000000000000000000000000000000000000000"

func TestEveryJobRPCRefusesAMalformedIDBeforeReadingAnything(t *testing.T) {
	h := newHarness(t)

	for name, call := range map[string]func(string) error{
		"get":    func(id string) error { _, err := h.job(id); return err },
		"cancel": h.cancel,
		"retry":  h.retry,
	} {
		t.Run(name, func(t *testing.T) {
			for _, bad := range []string{"", "../../etc/passwd", strings.Repeat("a", 63), strings.Repeat("A", 64), "-" + strings.Repeat("a", 63), "a/b"} {
				require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(call(bad)),
					"%q must be refused", bad)
			}
			require.Equal(t, connect.CodeNotFound, connect.CodeOf(call(unknownJobID)))
		})
	}
}

func TestEveryJobRPCIsLeaderOnly(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	h.store.stepDown()

	_, err := h.job(id)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(h.cancel(id)))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(h.retry(id)))
	require.Equal(t, connect.CodeUnavailable,
		connect.CodeOf(h.list("p", func(*plmv2.JobRef) error { return nil })))
}

func TestGetCarriesTheCidsOfEveryAttemptAndResource(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	rec := h.mustRecord(t, id)
	rec.Attempt = 2
	rec.Logs = map[string]string{stamp(1): "cid-log-1", stamp(2): "cid-log-2"}
	rec.Assets = map[string]string{"prod/website/home": "cid-a-2"}
	h.plantRecord(t, rec)

	job, err := h.job(id)
	require.NoError(t, err)

	require.Equal(t, id, job.GetId())
	require.Equal(t, plmv2.JobStatus_JobStatusOpen, job.GetStatus())
	require.Equal(t, testRepo, job.GetRepositoryKey())
	require.Equal(t, testRef, job.GetRef())
	require.Equal(t, testCommit, job.GetCommitSha())
	require.Equal(t, int32(2), job.GetAttempt())

	require.Equal(t, map[string]string{stamp(1): "cid-log-1", stamp(2): "cid-log-2"}, job.GetLogs(),
		"keyed by the attempt, which sorts causally — v1 keys this map by a wall clock")
	require.Equal(t, map[string]string{"prod/website/home": "cid-a-2"}, job.GetAssetCid())
}

func TestGetHandsOutACopyOfTheMapsAndNotTheRecords(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	rec := h.mustRecord(t, id)
	rec.Logs = map[string]string{stamp(1): "cid-log-1"}
	h.plantRecord(t, rec)

	first, err := h.job(id)
	require.NoError(t, err)
	first.GetLogs()["invented"] = "cid-nothing"

	second, err := h.job(id)
	require.NoError(t, err)
	require.Equal(t, map[string]string{stamp(1): "cid-log-1"}, second.GetLogs())
}

func TestGetAnswersAJobThatHasProducedNothingWithEmptyMaps(t *testing.T) {
	h := newHarness(t)

	job, err := h.job(h.planned(t, testRef, testCommit))
	require.NoError(t, err)
	require.NotNil(t, job.GetLogs())
	require.Empty(t, job.GetLogs())
	require.NotNil(t, job.GetAssetCid())
	require.Empty(t, job.GetAssetCid())
}

func TestListStreamsAProjectsJobIDs(t *testing.T) {
	h := newHarness(t)
	project := "project-of-" + testRepo

	want := map[string]bool{}
	for _, ref := range []string{"refs/heads/main", "refs/heads/next", "refs/heads/other"} {
		want[h.push(t, ref, testCommit)] = true
	}

	var got []string
	require.NoError(t, h.list(project, func(ref *plmv2.JobRef) error {
		got = append(got, ref.GetId())
		return nil
	}))

	require.Len(t, got, 3)
	for _, id := range got {
		require.True(t, want[id], "%s is not one of this project's jobs", id)
	}

	// Another project's listing is empty rather than everybody's.
	require.NoError(t, h.list("someone-else", func(*plmv2.JobRef) error {
		t.Fatal("a project must only list its own jobs")
		return nil
	}))
}

func TestListRefusesAnEmptyProjectAndPropagatesASendFailure(t *testing.T) {
	h := newHarness(t)
	h.planned(t, testRef, testCommit)

	require.Equal(t, connect.CodeInvalidArgument,
		connect.CodeOf(h.list("", func(*plmv2.JobRef) error { return nil })))

	sendErr := errors.New("client went away")
	require.ErrorIs(t, h.list("project-of-"+testRepo, func(*plmv2.JobRef) error { return sendErr }), sendErr)
}

func TestListReportsAProjectRowItCannotDecode(t *testing.T) {
	h := newHarness(t)
	h.records.put(t, projectRows("p")+"!!!", nil)

	require.Equal(t, connect.CodeInternal,
		connect.CodeOf(h.list("p", func(*plmv2.JobRef) error { return nil })))
}

func TestCancelEndsTheJobAndTakesTheItemOutOfTheQueue(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	require.NoError(t, h.cancel(id))
	require.Equal(t, plmv2.JobStatus_JobStatusCancelled, h.status(t, id))
	require.Empty(t, h.open(t, testRepo), "a finished job is not open")
	require.Len(t, h.terminal(t, id), 1)

	// The open row and the queue item are the two halves of one fact, so both
	// go: a repository told it has nothing outstanding, with a delivery still
	// queued for it, is the disagreement this layout exists to prevent.
	ready, claimed := h.work.Depth()
	require.Equal(t, 0, ready)
	require.Equal(t, 0, claimed)

	require.False(t, h.mustStep(t), "there is nothing left to claim")
	require.Empty(t, h.builder.seen(), "a cancelled job must not be run")
}

func TestACancelledJobLeavesNoWorkBehindItInTheQueue(t *testing.T) {
	t.Run("the builder reaches the live job in the very next cycle", func(t *testing.T) {
		h := newHarness(t)
		// IN PUSH ORDER, so the cancelled job is the one a claim would reach first.
		jobs := h.plannedEach(t, testRef, "refs/heads/other")
		cancelled, live := jobs[0], jobs[1]

		require.NoError(t, h.cancel(cancelled))

		ready, _ := h.work.Depth()
		require.Equal(t, 1, ready, "only the job somebody still wants built is queued")

		require.True(t, h.mustStep(t))
		require.Equal(t, plmv2.JobStatus_JobStatusSuccess, h.status(t, live),
			"a cancelled job must not spend the cycle the live one needed")
		require.Equal(t, plmv2.JobStatus_JobStatusCancelled, h.status(t, cancelled))
		require.Len(t, h.builder.seen(), 1)
	})

	t.Run("a leader that runs no builds claims nothing, so nothing would ever reap it", func(t *testing.T) {
		h := newHarness(t, withoutBuilders())
		id := h.push(t, testRef, testCommit)

		require.NoError(t, h.cancel(id))

		// This node never claims (withBuilders: a voter need not dispatch), so
		// the claim path cannot be what drops the item. Nothing else would.
		require.False(t, h.mustStep(t))
		ready, claimed := h.work.Depth()
		require.Equal(t, 0, ready)
		require.Equal(t, 0, claimed)
	})
}

func TestCancellingACancelledJobIsTheOutcomeTheCallerAskedFor(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	require.NoError(t, h.cancel(id))
	require.NoError(t, h.cancel(id))
}

func TestCancellingAFinishedJobIsAFailedPrecondition(t *testing.T) {
	for _, status := range []plmv2.JobStatus{
		plmv2.JobStatus_JobStatusSuccess, plmv2.JobStatus_JobStatusFailed,
	} {
		t.Run(status.String(), func(t *testing.T) {
			h := newHarness(t)
			id := h.planned(t, testRef, testCommit)
			require.NoError(t, h.finishNow(t, id, status, ""))

			require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(h.cancel(id)))
		})
	}
}

func TestCancellingAJobWhoseItemIsAlreadyGoneIsNotAnError(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.work.Ack(d))

	require.NoError(t, h.cancel(id))
	require.Equal(t, plmv2.JobStatus_JobStatusCancelled, h.status(t, id))
}

func TestCancelReportsAWithdrawalThatFailedAndTheNextCancelFinishesIt(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	// The cancelled event lands, and the withdrawal's queue entry does not.
	h.store.failBatches(errors.New("log is full"))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(h.cancel(id)))

	require.Equal(t, plmv2.JobStatus_JobStatusCancelled, h.status(t, id),
		"the event is what cancels the job, and it landed")
	ready, _ := h.work.Depth()
	require.Equal(t, 1, ready, "the item the entry did not remove is still there")

	h.store.failBatches(nil)
	require.NoError(t, h.cancel(id))

	ready, claimed := h.work.Depth()
	require.Equal(t, 0, ready)
	require.Equal(t, 0, claimed)
}

func TestCancelReportsAStoreThatRefusedTheWrite(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	h.records.failBatches(errors.New("the store is full"))

	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(h.cancel(id)))
}

func TestRetryOnANonTerminalJobIsRefusedRatherThanSilentlyIgnored(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(h.retry(id)))

	rec := h.mustRecord(t, id)
	rec.Status = int32(plmv2.JobStatus_JobStatusClaimed)
	rec.Attempt = 1
	h.plantRecord(t, rec)

	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(h.retry(id)))
}

func TestRetryReopensAndReQueuesWithoutLosingHistory(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	rec := h.mustRecord(t, id)
	rec.Attempt = 1
	rec.Logs = map[string]string{stamp(1): "cid-log-1"}
	h.plantRecord(t, rec)

	require.NoError(t, h.finishNow(t, id, plmv2.JobStatus_JobStatusFailed, "it broke"))

	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.work.Ack(d))

	require.NoError(t, h.retry(id))

	reopened := h.mustRecord(t, id)
	require.Equal(t, plmv2.JobStatus_JobStatusOpen, reopened.status())
	require.Equal(t, uint32(2), reopened.Attempt,
		"the counter keeps going up, it does not reset: the failure ended a second attempt")
	require.Empty(t, reopened.Reason)
	require.Len(t, h.open(t, testRepo), 1, "reopened means open again")

	ready, _ := h.work.Depth()
	require.Equal(t, 1, ready)

	job, err := h.job(id)
	require.NoError(t, err)
	require.Equal(t, map[string]string{stamp(1): "cid-log-1"}, job.GetLogs(),
		"the previous run's log survives a retry")
}

func TestRetryReportsAStoreThatRefusedTheWrite(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	require.NoError(t, h.finishNow(t, id, plmv2.JobStatus_JobStatusFailed, "x"))

	h.records.failBatches(errors.New("the store is full"))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(h.retry(id)))
}

func TestACorruptEventIsInternalOnEveryRead(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	h.records.garble(t, id)

	_, err := h.job(id)
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
	require.Equal(t, connect.CodeInternal, connect.CodeOf(h.cancel(id)))
	require.Equal(t, connect.CodeInternal, connect.CodeOf(h.retry(id)))
}

func TestRetryReportsAPushThatFailsAfterTheJobWasReopened(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	require.NoError(t, h.finishNow(t, id, plmv2.JobStatus_JobStatusFailed, "x"))

	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.work.Ack(d))

	h.store.failBatches(errors.New("log is full"))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(h.retry(id)))
}

func TestRetryRemovesTheRetentionRowItActuallyWrote(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	h.clock.advance(5 * time.Minute)
	require.NoError(t, h.finishNow(t, id, plmv2.JobStatus_JobStatusFailed, "x"))

	rec := h.mustRecord(t, id)
	require.NotEqual(t, rec.CreatedAt, rec.UpdatedAt, "a build that took no time proves nothing here")
	require.Equal(t, []string{terminalRow(time.Unix(rec.UpdatedAt, 0), id)}, h.terminal(t, id),
		"the retention row is keyed by the second the job says it finished")

	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.work.Ack(d))

	h.clock.advance(time.Hour)
	require.NoError(t, h.retry(id))

	require.Empty(t, h.terminal(t, id), "a reopened job is not retained history")
}

func TestASuccessfulBuildPointsTheDeploymentAtWhatItMade(t *testing.T) {
	h := newWireHarness(t)
	id := h.planned(t, testRef, testCommit)

	h.builder.run = func(ctx context.Context, w run.Work, out run.Output) error {
		return out.Asset(ctx, "website/home", strings.NewReader("the artifact"))
	}
	require.True(t, h.mustStep(t))

	cid, err := h.built(t, h.mustRecord(t, id).ProjectID, "prod", "website/home")
	require.NoError(t, err)
	require.Equal(t, assetCID([]byte("the artifact")), cid)

	// AND IT IS THE SAME CID THE RECORD CARRIES, because both come out of one
	// entry and one produced value.
	require.Equal(t, cid, h.mustRecord(t, id).Assets["prod/website/home"])
}

func TestADeploymentsArtifactIsItsOwn(t *testing.T) {
	h := newWireHarness(t)
	h.builder.plan = func(context.Context, run.Work, io.Writer) ([]string, error) {
		return []string{"prod", "preview"}, nil
	}
	plan := h.push(t, testRef, testCommit)

	// ONE JOB PER ENVIRONMENT, each building only its own.
	h.builder.run = func(ctx context.Context, w run.Work, out run.Output) error {
		content := map[string]string{"prod": "production", "preview": "preview"}[w.Environment]
		return out.Asset(ctx, "website/home", strings.NewReader(content))
	}
	require.True(t, h.mustStep(t), "the plan")
	require.Len(t, h.mustRecord(t, plan).Children, 2)
	require.True(t, h.mustStep(t))
	require.True(t, h.mustStep(t))

	project := h.mustRecord(t, plan).ProjectID
	production, err := h.built(t, project, "prod", "website/home")
	require.NoError(t, err)
	preview, err := h.built(t, project, "preview", "website/home")
	require.NoError(t, err)

	require.NotEqual(t, production, preview)
	require.Equal(t, assetCID([]byte("production")), production)
	require.Equal(t, assetCID([]byte("preview")), preview)

	// THE NODE THIS SERVICE RUNS ON reads the same row in-process.
	local, err := h.Built(project, "prod", "website/home")
	require.NoError(t, err)
	require.Equal(t, production, local)
	_, err = h.Built(project, "prod", "website/elsewhere")
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestAFailedBuildDoesNotMoveWhatADeploymentRuns(t *testing.T) {
	h := newWireHarness(t)
	id := h.planned(t, testRef, testCommit)

	h.builder.run = func(ctx context.Context, w run.Work, out run.Output) error {
		if err := out.Asset(ctx, "website/home", strings.NewReader("half a build")); err != nil {
			return err
		}
		return errors.New("the toolchain fell over")
	}
	require.True(t, h.mustStep(t))

	record := h.mustRecord(t, id)
	require.Equal(t, plmv2.JobStatus_JobStatusFailed, record.status())
	require.NotEmpty(t, record.Assets, "what the failed run produced is still on the record")

	_, err := h.built(t, record.ProjectID, "prod", "website/home")
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestArtifactRefusesWhatItCannotAsk(t *testing.T) {
	h := newWireHarness(t)

	for _, args := range [][3]string{
		{"", "prod", "website/home"},
		{"shop", "", "website/home"},
		{"shop", "prod", ""},
	} {
		_, err := h.built(t, args[0], args[1], args[2])
		require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), args)
	}

	// A RESOURCE NOTHING HAS BUILT IS NotFound, and that is not the same as a
	// resource that needs no build — an image is pulled and a component is
	// built as its own resource. Which of those was asked about is the caller's
	// to know.
	_, err := h.built(t, "shop", "prod", "website/home")
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	// And it is its own action, so an authorizer can tell a project read from a job
	// read.
	require.Contains(t, h.authorizer.acts, ActArtifact)
}
