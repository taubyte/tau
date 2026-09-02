package backend

import (
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
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

// The maps are a READ shape, assembled from one row per entry. Rows are read in
// attempt order, so a retry's asset for a resource wins over the first run's —
// which is what a reader of a retried job wants.
func TestGetAssemblesTheMapsFromOneRowPerEntryInAttemptOrder(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	first := &attemptSink{s: h.Service, id: id, attempt: 1}
	second := &attemptSink{s: h.Service, id: id, attempt: 2}

	at1 := h.clock.now()
	at2 := at1.Add(time.Minute)
	require.NoError(t, first.Log(t.Context(), at1, "cid-log-1"))
	require.NoError(t, first.Asset(t.Context(), "website/home", "cid-a-1"))
	require.NoError(t, second.Log(t.Context(), at2, "cid-log-2"))
	require.NoError(t, second.Asset(t.Context(), "website/home", "cid-a-2"))

	job, err := h.job(id)
	require.NoError(t, err)

	require.Equal(t, id, job.GetId())
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, job.GetStatus())
	require.Equal(t, testRepo, job.GetRepositoryKey())
	require.Equal(t, testRef, job.GetRef())
	require.Equal(t, testCommit, job.GetCommitSha())

	require.Equal(t, map[string]string{
		at1.Format(time.RFC3339Nano): "cid-log-1",
		at2.Format(time.RFC3339Nano): "cid-log-2",
	}, job.GetLogs())
	require.Equal(t, map[string]string{"website/home": "cid-a-2"}, job.GetAssetCid(),
		"a later attempt's asset must win")
}

func TestASinkRefusesARowThatWouldBeAnEmptySegment(t *testing.T) {
	h := newHarness(t)
	sink := &attemptSink{s: h.Service, id: h.push(t, testRef, testCommit), attempt: 1}

	require.Error(t, sink.Log(t.Context(), h.clock.now(), ""))
	require.Error(t, sink.Asset(t.Context(), "", "cid"))
	require.Error(t, sink.Asset(t.Context(), "r", ""))

	h.store.failBatches(errors.New("log is full"))
	require.Error(t, sink.Log(t.Context(), h.clock.now(), "cid"))
	require.Error(t, sink.Asset(t.Context(), "r", "cid"))
}

func TestGetReportsARowItCannotParse(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	h.store.put(t, attemptPrefix(id)+"nonsense", []byte("x"))

	_, err := h.job(id)
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
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

// A retry keeps the accumulated attempt rows: the record's counter keeps going
// up, so the new run writes under a higher attempt/ segment and the previous
// run's logs stay readable.
func TestRetryReopensAndReQueuesWithoutLosingHistory(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	rec := h.mustRecord(t, id)
	rec.Attempt = 1
	value, err := rec.encode()
	require.NoError(t, err)
	h.store.put(t, jobRecordKey(id), value)

	sink := &attemptSink{s: h.Service, id: id, attempt: 1}
	require.NoError(t, sink.Log(t.Context(), h.clock.now(), "cid-log-1"))

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
	require.Len(t, job.GetLogs(), 1, "the previous run's logs survive a retry")
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

// A row listed and then gone is a store that changed under the listing, not a
// fault: a job read must not fail because somebody wrote beside it.
func TestGetSkipsARowThatVanishedBetweenTheListingAndTheRead(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	sink := &attemptSink{s: h.Service, id: id, attempt: 1}
	require.NoError(t, sink.Asset(t.Context(), "website/home", "cid-a"))
	h.store.haunt(attemptAssetKey(id, 1, "gone"))

	job, err := h.job(id)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"website/home": "cid-a"}, job.GetAssetCid())
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
// retained. The clock moves between the two here, which is the only way to see
// it.
func TestRetryRemovesTheRetentionRowItActuallyWrote(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	require.NoError(t, h.finishNow(t, id, buildv1.JobStatus_JobStatusFailed, "x"))

	terminal := rootSegment + "/" + indexSegment + "/" + terminalSegment + "/"
	require.Len(t, h.store.Keys(terminal), 1)

	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.work.Ack(d))

	h.clock.advance(time.Hour)
	require.NoError(t, h.retry(id))

	require.Empty(t, h.store.Keys(terminal), "a reopened job is not retained history")
}
