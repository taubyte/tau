package backend

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
)

// queueKeyRoot is the prefix every key pkg/queue writes lives under
// (pkg/queue/store.go: "queue/<name>/..."). A test in this package cannot
// name one of those keys — the keyspace is unexported — but it can name the
// root, which is all a gate needs.
const queueKeyRoot = "queue/"

// ackpairing_test.go is the OTHER half of lostupdate_test.go's pairs: not two
// record writes racing, but a record write and the QUEUE WRITE THAT PAIRS WITH
// IT, decided by one read and made in two places.
//
// pkg/queue's Push deduplicates against the index row of a CLAIMED item
// ("if _, exists := store.Get(q.keys.index(id)); exists { return nil }") and
// Ack DELETES that very row. So a push that lands while this node holds a
// delivery is a no-op, and the ack that follows destroys the item it was
// deduplicated against — leaving a job with no item at all. The caller was told
// its retry was accepted.
//
// NEITHER OF THESE IS A TIGHT RACE. The first is two ordinary RPCs while a
// build is in flight, and the window is the whole length of the build.

// A cancel, then a retry, then the settle of the run the cancel stopped. Every
// step is an ordinary call and the order is fixed by the test, not by a gate.
func TestARetryBetweenACancelAndTheSettleOfTheRunItCancelledSurvives(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	d, err := h.work.Claim()
	require.NoError(t, err)

	building := make(chan struct{})
	release := make(chan struct{})
	h.runner.run = func(ctx context.Context, _ Work, _ Output) error {
		close(building)
		<-release
		// The cancel is what ended this build, and this is what a Runner that
		// honours its context returns.
		return ctx.Err()
	}

	ran := make(chan error, 1)
	go func() { ran <- h.run(context.Background(), d) }()
	<-building

	require.NoError(t, h.cancel(id), "the caller cancels the build")
	require.NoError(t, h.retry(id), "and then asks for it again: 202 Accepted")

	close(release)
	require.NoError(t, <-ran)

	rec := h.mustRecord(t, id)
	ready, claimed := h.work.Depth()
	open := len(h.store.Keys(openIndexPrefix(testRepo)))
	t.Logf("status=%s reason=%q attempt=%d ready=%d claimed=%d openRows=%d",
		rec.status(), rec.Reason, rec.Attempt, ready, claimed, open)

	require.Equal(t, buildv2.JobStatus_JobStatusOpen, rec.status(),
		"the retry was accepted, so the job the caller sees must be open")
	require.NotEqual(t, context.Canceled.Error(), rec.Reason,
		"and must not carry the outcome of the build it cancelled")
	require.Equal(t, 1, ready+claimed,
		"a job a caller was told is queued must have an item")
	require.Equal(t, 1, open,
		"and a row reconcile can find, or nothing repairs it for VisibilityTimeout+BuildTimeout")

	// PROGRESS THE CALLER CAN SEE, not just a tidy pair of rows: the next cycle
	// claims the retried job and builds it.
	h.runner.run = nil
	require.True(t, h.mustStep(t))
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, h.status(t, id))
	require.Len(t, h.runner.seen(), 2, "the retry is a second build, not the first one re-reported")
	require.Equal(t, testCommit, h.lastCommit(testRepo, testRef))
}

// THE DROP PATH IS THE SAME DEFECT ONE BRANCH OVER. begin re-reads a job that
// has already finished and decides to drop the delivery — and the write that
// decision makes is an ACK. Made after the section closed, a retry in the
// window is deduplicated against the very item the ack then destroys.
//
// THE RACER IS RELEASED AT THE RECORD READ, WHICH IS WHERE THE DECISION IS
// TAKEN. Armed inside the ack instead — on the first read of the queue's own
// keyspace — it reproduces nothing: pkg/queue takes its GROUP LOCK before it
// touches the store (Ack is group.mu.Lock() and then prepare()), so by then the
// racer's Push can only queue up behind, the push lands after the ack however
// this package is written, and the job comes out correct either way. The first
// version of this test learned that the expensive way: it asserted only the
// outcome and passed 2000 runs of the exact code it was written to catch.
//
// FROM THE READ, THE RACER CONTENDS FOR THE SECTION rather than for the queue's
// internals, which is the thing that decides the outcome: the retry blocks on
// jobLock, and with the ack inside the section that read it gets the lock only
// after the ack, so its push mints a real item and the job it reopened has one.
// That is what ready+claimed counts below.
//
// AND WHAT THIS STILL CANNOT SEE, said plainly, because the last two rounds
// each learned it the expensive way. Move the ack into a SECTION OF ITS OWN —
// the drop decided in one, the ack carrying it out in another — and this test
// passes: measured, five runs out of five. The window between the two sections
// holds no store operation for a gate to fire on, and sync.Mutex does not hand
// the lock to a waiter on the first unlock, so the ack wins the re-entry every
// time and the outcome is right by scheduling. That shape is refused by the ack
// taking a READING rather than a section (section.go), is unrepresentable
// without a second path to queue.Ack, and a second path is what
// TestEveryQueueWriteIsInsideTheSectionThatDecidedIt enumerates. An outcome
// test is not the guard for it and is not written as though it were.
//
// BUT THE HARM IS NO LONGER WHAT THIS FAILS ON. The ack takes the READ that
// decided it (section.go), so an ack carried out after that read's section has
// been left is REFUSED rather than made — the mutant that puts the defect back
// does not destroy an item quietly, it returns an error from run(), and the
// require.NoError below is what catches it. That is an outcome, in this
// package, with no seam in pkg/queue and no lock to win: measured, the
// leave-and-re-enter form of this defect fails at that line rather than at the
// needle. TestTheQueueWritesAreRefusedInsideADifferentLiveSection asserts the
// same thing from the other side — including while a DIFFERENT section is
// running, which is the form that reached the ack through a live number — and
// asserts that the delivery survives.
//
// The needle stays anyway, underneath, because it says something the outcome
// does not: at the instant the ack touches the queue's keyspace, jobLock is
// still held. It is no longer the only thing holding this test up — and it was
// never sufficient on its own, because a section released and retaken answers
// it the same way a section held all along does. It asks Held(), which is a
// bool: the question "is a section running" has an answer nothing can spend,
// and the old needle TryLocked, which ENTERS a section of its own to find out.
func TestTheDropDecisionAndItsAckAreOneSection(t *testing.T) {
	h, gate := gatedHarness(t)
	id := h.push(t, testRef, testCommit)

	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.cancel(id), "so this delivery is one to drop, not one to run")

	// The needle, at the instant the ack touches the queue's keyspace.
	var loose atomic.Bool
	gate.onFirstUnder(t, queueKeyRoot, func() {
		if !jobLock.Held() {
			loose.Store(true)
		}
	})

	// And the racer, at the read the drop is decided on.
	var retryErr error
	retried := gate.onRead(t, jobRecordKey(id), 1, func() { retryErr = h.retry(id) })

	require.NoError(t, h.run(context.Background(), d))
	<-retried
	require.NoError(t, retryErr, "the caller was told its retry was accepted")

	rec := h.mustRecord(t, id)
	ready, claimed := h.work.Depth()
	open := len(h.store.Keys(openIndexPrefix(testRepo)))
	t.Logf("sectionHeldAtTheAck=%v status=%s ready=%d claimed=%d openRows=%d",
		!loose.Load(), rec.status(), ready, claimed, open)

	require.False(t, loose.Load(),
		"the ack carries out what the drop decision read, and must be made inside the section that read it")
	require.Equal(t, buildv2.JobStatus_JobStatusOpen, rec.status())
	require.Equal(t, 1, ready+claimed,
		"a reopened job must have an item, not wait out VisibilityTimeout+BuildTimeout")
	require.Equal(t, 1, open)
	require.Empty(t, h.runner.seen(), "and nothing must have been built")
}
