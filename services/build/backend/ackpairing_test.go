package backend

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
)

// queueKeyRoot is the prefix every key pkg/v2/queue writes lives under
// (pkg/v2/queue/store.go: "queue/<name>/..."). A test in this package cannot
// name one of those keys — the keyspace is unexported — but it can name the
// root, which is all a gate needs.
const queueKeyRoot = "queue/"

// ackpairing_test.go is the OTHER half of lostupdate_test.go's pairs: not two
// record writes racing, but a record write and the QUEUE WRITE THAT PAIRS WITH
// IT, decided by one read and made in two places.
//
// pkg/v2/queue's Push deduplicates against the index row of a CLAIMED item
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
	h.runner.run = func(ctx context.Context, _ Work, _ Sink) error {
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

	require.Equal(t, buildv1.JobStatus_JobStatusOpen, rec.status(),
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
	require.Equal(t, buildv1.JobStatus_JobStatusSuccess, h.status(t, id))
	require.Len(t, h.runner.seen(), 2, "the retry is a second build, not the first one re-reported")
	require.Equal(t, testCommit, h.lastCommit(testRepo, testRef))
}

// THE DROP PATH IS THE SAME DEFECT ONE BRANCH OVER. begin re-reads a job that
// has already finished and decides to drop the delivery — and the write that
// decision makes is an ACK. Made after the section closed, a retry in the
// window is deduplicated against the very item the ack then destroys.
//
// THE OUTCOME CANNOT BE THE NEEDLE HERE, and the first version of this test
// learned that the expensive way: it asserted only the outcome and passed 2000
// runs of the exact code it was written to catch. pkg/v2/queue takes its GROUP
// LOCK before it touches the store — Ack is group.mu.Lock() and then prepare()
// — so the earliest instant a store gate can fire inside the ack is an instant
// at which the group lock is already held, and the racer's Push queues up
// behind it. The push therefore lands AFTER the ack however this package is
// written, the ack is a real one rather than a deduplicated no-op, and the job
// comes out correct either way. The harm needs the racer to win a lock a test
// cannot make it win.
//
// So the needle is the fact the outcome rests on: at the instant the ack
// touches the queue's keyspace, jobLock — the section whose read decided to ack
// — is still held. A goroutine that can take it is a section that has already
// closed. The outcome is asserted underneath, because it is what the section is
// for.
func TestTheDropDecisionAndItsAckAreOneSection(t *testing.T) {
	h, gate := gatedHarness(t)
	id := h.push(t, testRef, testCommit)

	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.cancel(id), "so this delivery is one to drop, not one to run")

	var loose atomic.Bool
	var retryErr error
	retried := gate.onFirstUnder(t, queueKeyRoot, func() {
		if jobLock.TryLock() {
			loose.Store(true)
			jobLock.Unlock()
		}
		retryErr = h.retry(id)
	})

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
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, rec.status())
	require.Equal(t, 1, ready+claimed,
		"a reopened job must have an item, not wait out VisibilityTimeout+BuildTimeout")
	require.Equal(t, 1, open)
	require.Empty(t, h.runner.seen(), "and nothing must have been built")
}
