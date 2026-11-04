package jobs

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/services/build/run"
)

// queueKeyRoot is the prefix every key pkg/queue writes lives under
// (pkg/queue/store.go: "queue/<name>/..."). A test in this package cannot
// name one of those keys — the keyspace is unexported — but it can name the
// root, which is all a gate needs.
const queueKeyRoot = "queue/"

// A job's event and the queue write paired with it. pkg/queue's Push dedups
// against a claimed item's index row and Ack deletes that row, so a push during a
// delivery followed by the ack would leave a job with no item. The window is a
// whole build.

func TestARetryBetweenACancelAndTheSettleOfTheRunItCancelledSurvives(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	d, err := h.work.Claim()
	require.NoError(t, err)

	building := make(chan struct{})
	release := make(chan struct{})
	h.runner.run = func(ctx context.Context, _ run.Work, _ run.Output) error {
		close(building)
		<-release
		// The cancel is what ended this build, and this is what a Runner that
		// honours its context returns.
		return ctx.Err()
	}

	ran := make(chan error, 1)
	go func() { ran <- h.run(context.Background(), d, h.runner) }()
	<-building

	require.NoError(t, h.cancel(id), "the caller cancels the build")
	require.NoError(t, h.retry(id), "and then asks for it again: 202 Accepted")

	close(release)
	require.NoError(t, <-ran)

	rec := h.mustRecord(t, id)
	ready, claimed := h.work.Depth()
	open := len(h.open(t, testRepo))
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
	require.Equal(t, testCommit, h.lastCommit(testRepo, testRef, testEnvironment))
}

func TestTheDropDecisionAndItsAckAreOneSection(t *testing.T) {
	h, gate := gatedHarness(t)
	id := h.planned(t, testRef, testCommit)

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
	retried := gate.onRead(t, eventPrefix(id), 1, func() { retryErr = h.retry(id) })

	require.NoError(t, h.run(context.Background(), d, h.runner))
	<-retried
	require.NoError(t, retryErr, "the caller was told its retry was accepted")

	rec := h.mustRecord(t, id)
	ready, claimed := h.work.Depth()
	open := len(h.open(t, testRepo))
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
