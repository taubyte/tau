package backend

import (
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	buildv1 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v1"
)

const unknownJobID = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// A caller-supplied id is refused for SHAPE before a single store read, so it
// never reaches a key builder.
func TestEveryJobRPCRefusesAMalformedIDBeforeReadingAnything(t *testing.T) {
	h := newHarness(t)

	for name, call := range map[string]func(string) error{
		"get":    func(id string) error { _, err := h.job(id); return err },
		"cancel": h.cancel,
		"retry":  h.retry,
	} {
		t.Run(name, func(t *testing.T) {
			for _, bad := range []string{"", "../../etc/passwd", strings.Repeat("A", 42), "a/b"} {
				require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(call(bad)),
					"%q must be refused", bad)
			}
			require.Equal(t, connect.CodeNotFound, connect.CodeOf(call(unknownJobID)))
		})
	}
}

func TestEveryJobRPCIsLeaderOnly(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	h.store.stepDown()

	_, err := h.job(id)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(h.cancel(id)))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(h.retry(id)))
	require.Equal(t, connect.CodeUnavailable,
		connect.CodeOf(h.list("p", func(*buildv1.JobRef) error { return nil })))
}

// THE MAPS ARE A READ SHAPE OFF THE RECORD, which is one Get rather than a
// prefix listing plus a Get per row. What they hold is one cid per attempt's
// log and one per resource — content ids of things in the asset store, not the
// content, and not a row each in raft.
func TestGetCarriesTheCidsOfEveryAttemptAndResource(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	rec := h.mustRecord(t, id)
	rec.Attempt = 2
	rec.Logs = map[string]string{stamp(1): "cid-log-1", stamp(2): "cid-log-2"}
	rec.Assets = map[string]string{"website/home": "cid-a-2"}
	value, err := rec.encode()
	require.NoError(t, err)
	h.store.put(t, jobRecordKey(id), value)

	job, err := h.job(id)
	require.NoError(t, err)

	require.Equal(t, id, job.GetId())
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, job.GetStatus())
	require.Equal(t, testRepo, job.GetRepositoryKey())
	require.Equal(t, testRef, job.GetRef())
	require.Equal(t, testCommit, job.GetCommitSha())
	require.Equal(t, int32(2), job.GetAttempt())

	require.Equal(t, map[string]string{stamp(1): "cid-log-1", stamp(2): "cid-log-2"}, job.GetLogs(),
		"keyed by the attempt, which sorts causally — v1 keys this map by a wall clock")
	require.Equal(t, map[string]string{"website/home": "cid-a-2"}, job.GetAssetCid())
}

// AND THE MAPS A CALLER IS HANDED ARE NOT THE RECORD'S. Two reads of one job
// must not share a map with each other or with the next write of that record.
func TestGetHandsOutACopyOfTheMapsAndNotTheRecords(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	rec := h.mustRecord(t, id)
	rec.Logs = map[string]string{stamp(1): "cid-log-1"}
	value, err := rec.encode()
	require.NoError(t, err)
	h.store.put(t, jobRecordKey(id), value)

	first, err := h.job(id)
	require.NoError(t, err)
	first.GetLogs()["invented"] = "cid-nothing"

	second, err := h.job(id)
	require.NoError(t, err)
	require.Equal(t, map[string]string{stamp(1): "cid-log-1"}, second.GetLogs())
}

// A job with no runs behind it reads as empty maps rather than as nulls, which
// is what v1's shape promises a caller.
func TestGetAnswersAJobThatHasProducedNothingWithEmptyMaps(t *testing.T) {
	h := newHarness(t)

	job, err := h.job(h.push(t, testRef, testCommit))
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
	require.NoError(t, h.list(project, func(ref *buildv1.JobRef) error {
		got = append(got, ref.GetId())
		return nil
	}))

	require.Len(t, got, 3)
	for _, id := range got {
		require.True(t, want[id], "%s is not one of this project's jobs", id)
	}

	// Another project's listing is empty rather than everybody's.
	require.NoError(t, h.list("someone-else", func(*buildv1.JobRef) error {
		t.Fatal("a project must only list its own jobs")
		return nil
	}))
}

func TestListRefusesAnEmptyProjectAndPropagatesASendFailure(t *testing.T) {
	h := newHarness(t)
	h.push(t, testRef, testCommit)

	require.Equal(t, connect.CodeInvalidArgument,
		connect.CodeOf(h.list("", func(*buildv1.JobRef) error { return nil })))

	sendErr := errors.New("client went away")
	require.ErrorIs(t, h.list("project-of-"+testRepo, func(*buildv1.JobRef) error { return sendErr }), sendErr)
}

func TestListReportsAnIndexRowItCannotDecode(t *testing.T) {
	h := newHarness(t)
	h.store.put(t, projectIndexPrefix("p")+"!!!", nil)

	require.Equal(t, connect.CodeInternal,
		connect.CodeOf(h.list("p", func(*buildv1.JobRef) error { return nil })))
}

// Cancel is a record write and the withdrawal of the item that record was
// written for. A queued job needs no special case beyond that: the claimer
// re-reads the record before it runs, so a delivery that outruns the withdrawal
// is dropped rather than built.
func TestCancelWritesTheRecordAndTakesTheItemOutOfTheQueue(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	require.NoError(t, h.cancel(id))
	require.Equal(t, buildv1.JobStatus_JobStatusCancelled, h.status(t, id))
	require.Empty(t, h.store.Keys(openIndexPrefix(testRepo)), "a finished job is not open")
	require.Len(t, h.store.Keys(rootSegment+"/"+indexSegment+"/"+terminalSegment+"/"), 1)

	// The open row and the queue item are the two halves of one fact, so both
	// go: a repository told it has nothing outstanding, with a delivery still
	// queued for it, is the disagreement this layout exists to prevent.
	ready, claimed := h.work.Depth()
	require.Equal(t, 0, ready)
	require.Equal(t, 0, claimed)

	require.False(t, h.mustStep(t), "there is nothing left to claim")
	require.Empty(t, h.runner.seen(), "a cancelled job must not be run")
}

// WHAT THE LEFTOVER ITEM ACTUALLY COSTS, which is why the withdrawal is not
// tidiness. Both halves are the same orphan seen from two deployments.
func TestACancelledJobLeavesNoWorkBehindItInTheQueue(t *testing.T) {
	t.Run("the builder reaches the live job in the very next cycle", func(t *testing.T) {
		h := newHarness(t)
		cancelled := h.push(t, testRef, testCommit)

		// A second later, so the ready keys order by push and the cancelled job
		// is the one a claim would reach first.
		h.clock.advance(time.Second)
		live := h.push(t, "refs/heads/other", testCommit)

		require.NoError(t, h.cancel(cancelled))

		ready, _ := h.work.Depth()
		require.Equal(t, 1, ready, "only the job somebody still wants built is queued")

		require.True(t, h.mustStep(t))
		require.Equal(t, buildv1.JobStatus_JobStatusSuccess, h.status(t, live),
			"a cancelled job must not spend the cycle the live one needed")
		require.Equal(t, buildv1.JobStatus_JobStatusCancelled, h.status(t, cancelled))
		require.Len(t, h.runner.seen(), 1)
	})

	t.Run("a leader that runs no builds claims nothing, so nothing would ever reap it", func(t *testing.T) {
		h := newHarness(t, func(s *Service) error { s.runner = nil; return nil })
		id := h.push(t, testRef, testCommit)

		require.NoError(t, h.cancel(id))

		// This node never claims (WithRunner: a voter need not be a builder), so
		// the claim path cannot be what drops the item. Nothing else would.
		require.False(t, h.mustStep(t))
		ready, claimed := h.work.Depth()
		require.Equal(t, 0, ready)
		require.Equal(t, 0, claimed)
	})
}

func TestCancellingACancelledJobIsTheOutcomeTheCallerAskedFor(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	require.NoError(t, h.cancel(id))
	require.NoError(t, h.cancel(id))
}

func TestCancellingAFinishedJobIsAFailedPrecondition(t *testing.T) {
	for _, status := range []buildv1.JobStatus{
		buildv1.JobStatus_JobStatusSuccess, buildv1.JobStatus_JobStatusFailed,
	} {
		t.Run(status.String(), func(t *testing.T) {
			h := newHarness(t)
			id := h.push(t, testRef, testCommit)
			require.NoError(t, h.finishNow(t, id, status, ""))

			require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(h.cancel(id)))
		})
	}
}

// An item the queue no longer holds is nothing to withdraw, not a failure: it
// is a delivery already acked by a run whose terminal write never landed.
func TestCancellingAJobWhoseItemIsAlreadyGoneIsNotAnError(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.work.Ack(d))

	require.NoError(t, h.cancel(id))
	require.Equal(t, buildv1.JobStatus_JobStatusCancelled, h.status(t, id))
}

// The withdrawal is a second entry and can fail on its own. The record is what
// cancels the job, so it is written first and the failure is reported — and the
// caller's retry is what finishes the withdrawal, which is why cancelling a
// cancelled job is a repair rather than a no-op.
func TestCancelReportsAWithdrawalThatFailedAndTheNextCancelFinishesIt(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	// The record's entry commits, and the withdrawal's does not.
	h.store.onBatch(func() { h.store.failBatches(errors.New("log is full")) })
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(h.cancel(id)))

	require.Equal(t, buildv1.JobStatus_JobStatusCancelled, h.status(t, id),
		"the record is what cancels the job, and it landed")
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
	id := h.push(t, testRef, testCommit)
	h.store.failBatches(errors.New("log is full"))

	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(h.cancel(id)))
}

// v1's retry looked the job up, found it, and answered {"retry": jid} without
// doing anything — so "retry" on a running job was a lie the caller could not
// detect.
func TestRetryOnANonTerminalJobIsRefusedRatherThanSilentlyIgnored(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(h.retry(id)))

	rec := h.mustRecord(t, id)
	rec.Status = int32(buildv1.JobStatus_JobStatusClaimed)
	value, err := rec.encode()
	require.NoError(t, err)
	h.store.put(t, jobRecordKey(id), value)

	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(h.retry(id)))
}

// A retry keeps what earlier attempts produced: the record's counter keeps
// going up, so the next run's log is recorded under a higher attempt and the
// previous one's cid stays where it was.
func TestRetryReopensAndReQueuesWithoutLosingHistory(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	rec := h.mustRecord(t, id)
	rec.Attempt = 1
	rec.Logs = map[string]string{stamp(1): "cid-log-1"}
	value, err := rec.encode()
	require.NoError(t, err)
	h.store.put(t, jobRecordKey(id), value)

	require.NoError(t, h.finishNow(t, id, buildv1.JobStatus_JobStatusFailed, "it broke"))

	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.work.Ack(d))

	require.NoError(t, h.retry(id))

	reopened := h.mustRecord(t, id)
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, reopened.status())
	require.Equal(t, uint32(1), reopened.Attempt, "the counter keeps going up, it does not reset")
	require.Empty(t, reopened.Reason)
	require.Len(t, h.store.Keys(openIndexPrefix(testRepo)), 1, "reopened means open again")

	ready, _ := h.work.Depth()
	require.Equal(t, 1, ready)

	job, err := h.job(id)
	require.NoError(t, err)
	require.Equal(t, map[string]string{stamp(1): "cid-log-1"}, job.GetLogs(),
		"the previous run's log survives a retry")
}

func TestRetryReportsAStoreThatRefusedTheWrite(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	require.NoError(t, h.finishNow(t, id, buildv1.JobStatus_JobStatusFailed, "x"))

	h.store.failBatches(errors.New("log is full"))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(h.retry(id)))
}

func TestACorruptRecordIsInternalOnEveryRead(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	h.store.put(t, jobRecordKey(id), []byte("not cbor"))

	_, err := h.job(id)
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
	require.Equal(t, connect.CodeInternal, connect.CodeOf(h.cancel(id)))
	require.Equal(t, connect.CodeInternal, connect.CodeOf(h.retry(id)))
}

// The record's push is a second entry, so it can fail on its own.
func TestRetryReportsAPushThatFailsAfterTheRecordWasReopened(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	require.NoError(t, h.finishNow(t, id, buildv1.JobStatus_JobStatusFailed, "x"))

	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.work.Ack(d))

	h.store.onBatch(func() { h.store.failBatches(errors.New("log is full")) })
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(h.retry(id)))
}

// The retention row is written at the moment a job finishes, so a retry that
// looked it up under the reopening's timestamp would delete a key nothing wrote
// and leave the real row behind — a job that reads Open and is also listed as
// retained.
//
// THE CLOCK MOVES TWICE HERE, AND BOTH MOVES ARE THE TEST.
//
// The second — between the finish and the retry — is what makes the reopening's
// timestamp a different key from the one the finish wrote. It has been here
// since the round that spelled the delete by hand.
//
// THE FIRST IS THE NINTH ROUND'S, and without it this test was named for an
// invariant it could not see. A job built instantly has CreatedAt == UpdatedAt,
// so `retained` stamping time.Unix(rec.CreatedAt, 0) and `dropRetained` reading
// rec.CreatedAt are BOTH invisible: two independent one-token substitutions,
// each reading a plausible timestamp off the right record, and each green
// across twenty runs of the whole service. Every other clock.advance in this
// package expires a claim or ages a reconcile row; none of them ever moved the
// clock between a job's creation and its terminal write, so nothing in the
// package ever wrote a terminal record whose two timestamps differed. Five
// minutes here is a build that took five minutes, and it is what makes the
// retention row's key a fact about when the job FINISHED.
func TestRetryRemovesTheRetentionRowItActuallyWrote(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	h.clock.advance(5 * time.Minute)
	require.NoError(t, h.finishNow(t, id, buildv1.JobStatus_JobStatusFailed, "x"))

	terminal := rootSegment + "/" + indexSegment + "/" + terminalSegment + "/"
	rec := h.mustRecord(t, id)
	require.NotEqual(t, rec.CreatedAt, rec.UpdatedAt, "a build that took no time proves nothing here")
	require.Equal(t, []string{terminalIndexKey(time.Unix(rec.UpdatedAt, 0), id)}, h.store.Keys(terminal),
		"the retention row is keyed by the second the record says the job finished")

	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.work.Ack(d))

	h.clock.advance(time.Hour)
	require.NoError(t, h.retry(id))

	require.Empty(t, h.store.Keys(terminal), "a reopened job is not retained history")
}
