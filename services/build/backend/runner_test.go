package backend

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
	"github.com/taubyte/tau/pkg/v2/queue"
	"github.com/taubyte/tau/services/build"
)

func TestOneStepClaimsRunsAcksAndFinishes(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	var during buildv1.JobStatus
	h.runner.run = func(_ context.Context, w Work, sink Sink) error {
		during = h.status(t, w.JobID)
		return sink.Asset(context.Background(), "website/home", "cid-asset")
	}

	require.True(t, h.mustStep(t))

	require.Equal(t, buildv1.JobStatus_JobStatusClaimed, during,
		"the record must say Claimed while the run is going")

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv1.JobStatus_JobStatusSuccess, rec.status())
	require.Equal(t, uint32(1), rec.Attempt)
	require.Zero(t, rec.ClaimedUntil)
	require.Empty(t, rec.Holder)

	ready, claimed := h.work.Depth()
	require.Equal(t, 0, ready)
	require.Equal(t, 0, claimed)

	require.Empty(t, h.store.Keys(openIndexPrefix(testRepo)))

	work := h.runner.seen()
	require.Len(t, work, 1)
	require.Equal(t, Work{
		JobID: id, RepositoryKey: testRepo, Ref: testRef, CommitSHA: testCommit,
		Before: "", ProjectID: "project-of-" + testRepo, Attempt: 1,
	}, work[0])

	job, err := h.job(id)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"website/home": "cid-asset"}, job.GetAssetCid())
}

// The before..after anchor is this service's own: written on success, read at
// claim, never taken from the webhook. build.proto is explicit that `before` is
// exactly as webhook-supplied as `commit_sha` is, so carrying it would buy back
// no trust — the backend reads what it itself last finished.
func TestBeforeIsWhatThisServiceItselfLastFinished(t *testing.T) {
	h := newHarness(t)

	h.push(t, testRef, testCommit)
	require.True(t, h.mustStep(t))

	next := "89abcdef0123456789abcdef0123456789abcdef"
	h.push(t, testRef, next)
	require.True(t, h.mustStep(t))

	work := h.runner.seen()
	require.Len(t, work, 2)
	require.Empty(t, work[0].Before, "a ref with no history is a full build")
	require.Equal(t, testCommit, work[1].Before)

	// A different ref of the same repository has its own anchor.
	h.push(t, "refs/heads/other", testCommit)
	require.True(t, h.mustStep(t))
	require.Empty(t, h.runner.seen()[2].Before)
}

// AN ERROR FROM Run IS TERMINAL. The queue's retry exists for a worker that
// DIED, not for a build that failed — retrying a compile error five times is
// waste. v1's MaxJobAttempts counted only REPORTED failures, so a crash was
// free and a job that killed its worker retried forever.
func TestARunThatFailsIsTerminalRatherThanRetried(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	h.runner.run = func(context.Context, Work, Sink) error { return errors.New("compile error") }

	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv1.JobStatus_JobStatusFailed, rec.status())
	require.Contains(t, rec.Reason, "compile error")

	ready, claimed := h.work.Depth()
	require.Equal(t, 0, ready)
	require.Equal(t, 0, claimed)

	// And no anchor: a failed build must not become the commit the next one
	// diffs from.
	require.Empty(t, h.lastCommit(testRepo, testRef))
}

// A runner that panics must not take the loop down, and must not leave the item
// claimed until its lease expires either.
func TestARunnerThatPanicsIsAFailedBuild(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	h.runner.run = func(context.Context, Work, Sink) error { panic("nil map write") }

	require.True(t, h.mustStep(t))
	require.Equal(t, buildv1.JobStatus_JobStatusFailed, h.status(t, id))
	require.Contains(t, h.mustRecord(t, id).Reason, "panicked")
}

// Cancel while running: one record write, and the run's context ends in the
// same process. v1 needed three hops between two services for this.
func TestCancelWhileRunningStopsTheRunAndKeepsTheCancelledStatus(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	var ctxErr error
	h.runner.run = func(ctx context.Context, w Work, _ Sink) error {
		require.NoError(t, h.cancel(w.JobID))
		<-ctx.Done()
		ctxErr = ctx.Err()
		return ctx.Err()
	}

	require.True(t, h.mustStep(t))

	require.Equal(t, context.Canceled, ctxErr)
	require.Equal(t, buildv1.JobStatus_JobStatusCancelled, h.status(t, id),
		"a cancelled job must not be overwritten with the outcome of the run that was cancelled")
	require.Empty(t, h.lastCommit(testRepo, testRef))
}

// The claim's own deadline is the hard bound, and BuildTimeout is the policy on
// top. The smaller wins, so a lease made generous does not silently become the
// build timeout.
func TestTheRunIsBoundedByWhicheverOfTheLeaseAndThePolicyIsShorter(t *testing.T) {
	t.Run("the lease is the bound", func(t *testing.T) {
		h := newHarness(t, func(s *Service) error {
			s.cfg.BuildTimeout = time.Hour
			s.cfg.ClaimMargin = time.Minute
			return nil
		})
		h.push(t, testRef, testCommit)

		var left time.Duration
		h.runner.run = func(ctx context.Context, _ Work, _ Sink) error {
			deadline, ok := ctx.Deadline()
			require.True(t, ok, "a run must always be bounded")
			left = time.Until(deadline)
			return nil
		}
		require.True(t, h.mustStep(t))

		// VisibilityTimeout defaulted to BuildTimeout + DefaultClaimMargin, and
		// the margin here is a minute rather than the default.
		want := h.cfg.Queue.VisibilityTimeout - time.Minute
		require.InDelta(t, want.Seconds(), left.Seconds(), 5)
	})

	t.Run("the policy is the bound", func(t *testing.T) {
		h := newHarness(t, func(s *Service) error {
			s.cfg.BuildTimeout = time.Minute
			s.cfg.Queue.VisibilityTimeout = 24 * time.Hour
			return nil
		})
		h.push(t, testRef, testCommit)

		var left time.Duration
		h.runner.run = func(ctx context.Context, _ Work, _ Sink) error {
			deadline, ok := ctx.Deadline()
			require.True(t, ok, "a run must always be bounded")
			left = time.Until(deadline)
			return nil
		}
		require.True(t, h.mustStep(t))
		require.InDelta(t, time.Minute.Seconds(), left.Seconds(), 5,
			"a generous lease must not silently become the build timeout")
	})
}

// A RUN WHOSE CLAIM EXPIRED PUBLISHES NOTHING. Ack is the commit point and the
// only compare-and-swap this service has; the loser learns it there. v1 asked
// whether it still owned the job only when REPORTING, after it had already
// stashed assets and pushed keys to tns.
func TestARunWhoseClaimExpiredMidRunPublishesNothing(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	h.runner.run = func(context.Context, Work, Sink) error {
		// Somebody else takes the job while this run is going: the lease
		// elapses and the queue redelivers it.
		h.clock.advance(h.cfg.Queue.VisibilityTimeout + time.Second)
		_, err := h.work.Claim()
		require.NoError(t, err)
		return nil
	}

	_, err := h.step(t.Context())
	require.ErrorIs(t, err, queue.ErrStaleDelivery)

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv1.JobStatus_JobStatusClaimed, rec.status(),
		"the loser must not write a terminal status")
	require.Empty(t, h.lastCommit(testRepo, testRef), "and must not move the anchor")
}

// The same rule for the other way a claim stops being ours.
//
// SETTLE NOTICES AT ITS BARRIER NOW, not at its ack. The two are the same fact
// about the same instant — a node that is no longer the leader can neither
// barrier nor ack — and which of them speaks is decided by the order the
// section runs in: the barrier comes before the read the outcome is decided on,
// and the ack comes after it, carrying that read (section.go). What matters
// here is unchanged and is asserted below: NOTHING IS PUBLISHED.
func TestLeadershipLostMidRunPublishesNothing(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	h.runner.run = func(context.Context, Work, Sink) error {
		h.store.stepDown()
		return nil
	}

	_, err := h.step(t.Context())
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	require.ErrorContains(t, err, "not the leader of "+build.QueueDomain)
	require.Equal(t, buildv1.JobStatus_JobStatusClaimed, h.status(t, id))
	require.Empty(t, h.lastCommit(testRepo, testRef))

	_, claimed := h.work.Depth()
	require.Equal(t, 1, claimed, "and the delivery a follower could not settle comes back")
}

// A worker that DIED is the case redelivery exists for: the claim expires, the
// item comes back, and the attempt is consumed. v1's crashes were free.
func TestAWorkerThatDiedCostsAnAttemptAndTheJobComesBack(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	// A dead worker is a claim nobody ever acknowledges.
	d, err := h.work.Claim()
	require.NoError(t, err)
	require.Equal(t, uint32(1), d.Attempt)

	h.clock.advance(h.cfg.Queue.VisibilityTimeout + time.Second)

	require.True(t, h.mustStep(t))
	require.Equal(t, buildv1.JobStatus_JobStatusSuccess, h.status(t, id))
	require.Equal(t, uint32(1), h.mustRecord(t, id).Attempt,
		"the record counts the runs it actually saw")

	work := h.runner.seen()
	require.Len(t, work, 1)
	require.Equal(t, uint32(1), work[0].Attempt)
}

// A job that exhausts its attempts reaches the dead letter, and the drain is
// what stops it sitting there forever with a record that still reads Open.
func TestAnExhaustedJobIsDeadLetteredAndTheDrainMarksItFailed(t *testing.T) {
	h := newHarness(t, func(s *Service) error {
		s.cfg.Queue.MaxAttempts = 1
		q, err := s.group.Open(s.cfg.Queue)
		if err != nil {
			return err
		}
		s.work = q
		return nil
	})
	id := h.push(t, testRef, testCommit)

	// One delivery nobody acknowledges, then the lease elapses.
	_, err := h.work.Claim()
	require.NoError(t, err)
	h.clock.advance(h.cfg.Queue.VisibilityTimeout + time.Second)

	// The sweep in this cycle buries it; there is nothing left to claim.
	require.False(t, h.mustStep(t))
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id),
		"a buried job's record is still Open until something drains the dead letter")

	dead := h.deadLetter(t)
	ready, _ := dead.Depth()
	require.Equal(t, 1, ready)

	// The next cycle drains it.
	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv1.JobStatus_JobStatusFailed, rec.status())
	require.Equal(t, "attempts exhausted", rec.Reason)
	require.Empty(t, h.store.Keys(openIndexPrefix(testRepo)))

	ready, claimed := dead.Depth()
	require.Equal(t, 0, ready)
	require.Equal(t, 0, claimed)
	require.Empty(t, h.runner.seen(), "an exhausted job is never run again")
}

// A dead-letter entry whose record is already terminal is acknowledged without
// being rewritten.
func TestDrainingADeadLetterEntryForAFinishedJobJustAcksIt(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	require.NoError(t, h.finishNow(t, id, buildv1.JobStatus_JobStatusCancelled, "cancelled"))

	require.NoError(t, h.dead.Push(id, nil))
	require.True(t, h.mustStep(t))

	require.Equal(t, buildv1.JobStatus_JobStatusCancelled, h.status(t, id))
	ready, _ := h.dead.Depth()
	require.Equal(t, 0, ready)
}

// A queue item whose record is gone cannot be identified, let alone built, so
// it is acknowledged and dropped rather than redelivered forever.
func TestAnItemWithNoRecordIsAckedAndDropped(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, h.work.Push(unknownJobID, nil))

	require.True(t, h.mustStep(t))
	require.Empty(t, h.runner.seen())

	ready, claimed := h.work.Depth()
	require.Equal(t, 0, ready)
	require.Equal(t, 0, claimed)
}

// The ack-to-record gap healing itself: a redelivered job that is already
// finished is acked and dropped.
func TestARedeliveredJobThatIsAlreadyFinishedIsDropped(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	require.NoError(t, h.finishNow(t, id, buildv1.JobStatus_JobStatusSuccess, ""))

	require.True(t, h.mustStep(t))
	require.Empty(t, h.runner.seen())

	ready, claimed := h.work.Depth()
	require.Equal(t, 0, ready)
	require.Equal(t, 0, claimed)
}

// TWO BACKENDS OVER ONE DOMAIN: only the leader claims. That is the whole of
// the mutual exclusion, and it is why there is no claim RPC in build.proto.
func TestOnlyTheLeaderOfTheDomainClaims(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	follower, err := New(newHost(t, newMocknet(t)), h.store, h.repos, Config{},
		WithClock(h.clock.now), WithRunner(&fakeRunner{}))
	require.NoError(t, err)
	t.Cleanup(func() { follower.Close() })

	h.store.stepDown()

	_, err = follower.step(t.Context())
	require.ErrorIs(t, err, queue.ErrNotLeader)
	_, err = h.step(t.Context())
	require.ErrorIs(t, err, queue.ErrNotLeader)

	require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id))
	ready, claimed := h.work.Depth()
	require.Equal(t, 1, ready)
	require.Equal(t, 0, claimed)
}

// A node without a Runner still serves, still replicates and still keeps the
// queue swept — it just claims nothing. A voter need not be a builder.
func TestANodeWithNoRunnerKeepsTheQueueSweptAndClaimsNothing(t *testing.T) {
	store := newStore()
	clock := newClock()
	s, err := New(newHost(t, newMocknet(t)), store, newRepositories(testRepo), Config{}, WithClock(clock.now))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	id, err := s.enqueue(t.Context(), &buildv1.PushEvent{
		RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
	})
	require.NoError(t, err)

	worked, err := s.step(t.Context())
	require.NoError(t, err)
	require.False(t, worked)

	ready, claimed := s.work.Depth()
	require.Equal(t, 1, ready, "the item is left for a node that can run it")
	require.Equal(t, 0, claimed)

	r, err := s.record(unsectioned(), id)
	require.NoError(t, err)
	require.True(t, r.Found())
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, r.Value().status())
}

func TestStepReportsAStoreThatRefusesTheClaimWrite(t *testing.T) {
	h := newHarness(t)
	h.push(t, testRef, testCommit)

	// The record's Claimed write is the first Batch after the queue's own.
	h.store.onBatch(func() { h.store.failBatches(errors.New("log is full")) })

	_, err := h.step(t.Context())
	require.Error(t, err)
	require.Empty(t, h.runner.seen(), "a job whose claim could not be recorded must not run")
}

func TestStepReportsACorruptRecordRatherThanRunningIt(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	h.store.put(t, jobRecordKey(id), []byte("not cbor"))

	_, err := h.step(t.Context())
	require.Error(t, err)
	require.Empty(t, h.runner.seen())
}

// The loop is a ticker over step and holds no logic of its own, which is what
// makes every branch above reachable without a goroutine or a sleep. This is
// the one test that runs it.
func TestTheStartedLoopEventuallyRunsAQueuedJob(t *testing.T) {
	h := newHarness(t, func(s *Service) error {
		s.cfg.ClaimInterval = time.Millisecond
		return nil
	})
	id := h.push(t, testRef, testCommit)

	done := make(chan struct{})
	h.runner.run = func(context.Context, Work, Sink) error { close(done); return nil }

	require.NoError(t, h.Start(t.Context()))
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the claim loop never ran the job")
	}

	require.Eventually(t, func() bool {
		return h.status(t, id) == buildv1.JobStatus_JobStatusSuccess
	}, 5*time.Second, time.Millisecond)
}

// The store's own refusals, at each of the four places a cycle touches it. Each
// is a separate branch, and a cycle that swallowed any of them would look like
// a queue that quietly stopped draining.
func TestStepReportsEachOfTheStoresRefusals(t *testing.T) {
	t.Run("the dead letter cannot be read", func(t *testing.T) {
		h := newHarness(t)
		h.store.failBarrier(errors.New("timed out"))

		_, err := h.step(t.Context())
		require.ErrorContains(t, err, "dead letter claim")
	})

	t.Run("the dead letter cannot be claimed", func(t *testing.T) {
		h := newHarness(t)
		require.NoError(t, h.dead.Push(unknownJobID, nil))
		h.store.failBatches(errors.New("log is full"))

		_, err := h.step(t.Context())
		require.ErrorContains(t, err, "dead letter claim")
	})

	t.Run("the sweep cannot write", func(t *testing.T) {
		h := newHarness(t)
		h.push(t, testRef, testCommit)

		// An expired claim, so the sweep has something to move.
		_, err := h.work.Claim()
		require.NoError(t, err)
		h.clock.advance(h.cfg.Queue.VisibilityTimeout + time.Second)
		h.store.failBatches(errors.New("log is full"))

		_, err = h.step(t.Context())
		require.ErrorContains(t, err, "sweep")
	})

	t.Run("the claim cannot be written", func(t *testing.T) {
		h := newHarness(t)
		h.push(t, testRef, testCommit)
		h.store.failBatches(errors.New("log is full"))

		_, err := h.step(t.Context())
		require.ErrorContains(t, err, "claim")
		require.Empty(t, h.runner.seen())
	})
}

// The read the outcome is decided on can fail too, and a failure there must not
// be mistaken for "nothing to write".
//
// THE DELIVERY SURVIVES IT. The read comes before the ack now — it is what the
// ack carries out — so a record nothing can decode costs a redelivery rather
// than the item: this is the same answer the dead-letter drain gives a corrupt
// record one queue over, and the item is still bounded by MaxAttempts.
func TestSettleReportsARecordItCannotRead(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	h.runner.run = func(context.Context, Work, Sink) error {
		h.store.put(t, jobRecordKey(id), []byte("not cbor"))
		return nil
	}

	_, err := h.step(t.Context())
	require.ErrorContains(t, err, "corrupt job record")

	ready, claimed := h.work.Depth()
	require.Equal(t, 0, ready)
	require.Equal(t, 1, claimed, "a record nothing could read must not cost the item that names it")
}

// A dead-letter entry acknowledged by somebody else between the claim and the
// ack is reported rather than swallowed.
func TestTheDeadLetterDrainReportsAnAckItCannotMake(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	require.NoError(t, h.finishNow(t, id, buildv1.JobStatus_JobStatusCancelled, "cancelled"))
	require.NoError(t, h.dead.Push(id, nil))

	// The claim's own entry commits, and the ack's does not.
	h.store.onBatch(func() { h.store.failBatches(errors.New("log is full")) })

	_, err := h.step(t.Context())
	require.ErrorContains(t, err, "dead letter ack")
}

// The drain's own record write can fail, and the entry must then stay in the
// dead letter rather than being acknowledged as handled.
func TestTheDeadLetterDrainReportsARecordItCannotWrite(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	require.NoError(t, h.dead.Push(id, nil))

	h.store.onBatch(func() { h.store.failBatches(errors.New("log is full")) })

	_, err := h.step(t.Context())
	require.ErrorContains(t, err, "finishing job "+id)

	h.store.failBatches(nil)
	ready, claimed := h.dead.Depth()
	require.Equal(t, 0, ready)
	require.Equal(t, 1, claimed, "an entry whose record could not be written stays claimed and comes back")
}

// THE TWO CLAIM-LOOP SECTIONS TAKE A BARRIER OF THEIR OWN, and a barrier that
// cannot be taken is a section that must not decide. Both are reached only
// after a queue call has already barriered successfully, so the refusal has to
// be armed between the two — which is what the batch hook is for.
func TestASectionThatCannotBarrierDecidesNothing(t *testing.T) {
	t.Run("the claim's section", func(t *testing.T) {
		h := newHarness(t)
		id := h.push(t, testRef, testCommit)

		// The queue's own claim commits; begin's barrier is then refused.
		h.store.onBatch(func() { h.store.failBarrier(errors.New("timed out")) })

		_, err := h.step(t.Context())
		require.ErrorContains(t, err, "barrier on "+build.QueueDomain)
		require.Empty(t, h.runner.seen(), "a job whose section could not barrier must not run")
		require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id),
			"and its record must not have moved")
	})

	t.Run("the settle's section", func(t *testing.T) {
		h := newHarness(t)
		id := h.push(t, testRef, testCommit)

		// begin's barrier is taken and the build runs; settle's is refused.
		h.runner.run = func(context.Context, Work, Sink) error {
			h.store.failBarrier(errors.New("timed out"))
			return nil
		}

		_, err := h.step(t.Context())
		require.ErrorContains(t, err, "barrier on "+build.QueueDomain)

		h.store.failBarrier(nil)
		require.Equal(t, buildv1.JobStatus_JobStatusClaimed, h.status(t, id),
			"a section that could not barrier must not decide the outcome of the run")
		require.Empty(t, h.lastCommit(testRepo, testRef))
		_, claimed := h.work.Depth()
		require.Equal(t, 1, claimed, "and the delivery it did not ack comes back")
	})

	t.Run("the dead letter drain's section", func(t *testing.T) {
		h := newHarness(t)
		id := h.push(t, testRef, testCommit)
		require.NoError(t, h.dead.Push(id, nil))

		h.store.onBatch(func() { h.store.failBarrier(errors.New("timed out")) })

		_, err := h.step(t.Context())
		require.ErrorContains(t, err, "barrier on "+build.QueueDomain)

		h.store.failBarrier(nil)
		require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id))
		ready, claimed := h.dead.Depth()
		require.Equal(t, 0, ready)
		require.Equal(t, 1, claimed, "the entry stays claimed and comes back")
	})
}

// A dead-letter entry whose record will not decode is reported rather than
// treated as a job that has no record — which would ack it and lose the only
// thing that says the build ever happened.
func TestTheDeadLetterDrainReportsACorruptRecord(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	require.NoError(t, h.dead.Push(id, nil))
	h.store.put(t, jobRecordKey(id), []byte("not cbor"))

	_, err := h.step(t.Context())
	require.ErrorContains(t, err, "corrupt job record")

	ready, claimed := h.dead.Depth()
	require.Equal(t, 0, ready)
	require.Equal(t, 1, claimed, "and the entry is not acknowledged")
}
