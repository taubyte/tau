package backend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/queue"
	"github.com/taubyte/tau/services/build"
)

func TestOneStepClaimsRunsAcksAndFinishes(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	var during buildv2.JobStatus
	h.runner.run = func(_ context.Context, w Work, out Output) error {
		during = h.status(t, w.JobID)
		fmt.Fprintf(out, "building %s\n", w.CommitSHA)
		return out.Asset(context.Background(), "website/home", strings.NewReader("the artifact"))
	}

	require.True(t, h.mustStep(t))

	require.Equal(t, buildv2.JobStatus_JobStatusClaimed, during,
		"the record must say Claimed while the run is going")

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, rec.status())
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
		Before: "", ProjectID: "project-of-" + testRepo, Attempt: 1, Environment: testEnvironment,
	}, work[0])

	job, err := h.job(id)
	require.NoError(t, err)

	// WHAT THE RUN PRODUCED IS ON THE JOB AS CONTENT IDS, and the bytes are in
	// the asset store — one Put for the artifact, during the run, and one for
	// the log, at the end of it.
	require.Equal(t, map[string]string{"prod/website/home": assetCID([]byte("the artifact"))}, job.GetAssetCid())
	require.Equal(t, "the artifact", h.assets.content(t, job.GetAssetCid()["prod/website/home"]))

	logCID, logged := job.GetLogs()[stamp(1)]
	require.True(t, logged, "the log of attempt 1 is keyed by the attempt: %v", job.GetLogs())
	require.Equal(t, "building "+testCommit+"\n", h.assets.content(t, logCID))

	// And no file is left behind: the log is scratch, and what survives is the
	// cid above.
	require.Empty(t, logFiles(t, h), "the log file must not outlive the run that wrote it")
}

func TestBeforeIsWhatThisServiceItselfLastFinished(t *testing.T) {
	h := newHarness(t)

	h.planned(t, testRef, testCommit)
	require.True(t, h.mustStep(t))

	next := "89abcdef0123456789abcdef0123456789abcdef"
	h.planned(t, testRef, next)
	require.True(t, h.mustStep(t))

	work := h.runner.seen()
	require.Len(t, work, 2)
	require.Empty(t, work[0].Before, "a ref with no history is a full build")
	require.Equal(t, testCommit, work[1].Before)

	// A different ref of the same repository has its own anchor.
	h.planned(t, "refs/heads/other", testCommit)
	require.True(t, h.mustStep(t))
	require.Empty(t, h.runner.seen()[2].Before)
}

func TestARunThatFailsIsTerminalRatherThanRetried(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	h.runner.run = func(context.Context, Work, Output) error { return errors.New("compile error") }

	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusFailed, rec.status())
	require.Contains(t, rec.Reason, "compile error")

	ready, claimed := h.work.Depth()
	require.Equal(t, 0, ready)
	require.Equal(t, 0, claimed)

	// And no anchor: a failed build must not become the commit the next one
	// diffs from.
	require.Empty(t, h.lastCommit(testRepo, testRef, testEnvironment))
}

func TestARunnerThatPanicsIsAFailedBuild(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	h.runner.run = func(context.Context, Work, Output) error { panic("nil map write") }

	require.True(t, h.mustStep(t))
	require.Equal(t, buildv2.JobStatus_JobStatusFailed, h.status(t, id))
	require.Contains(t, h.mustRecord(t, id).Reason, "panicked")
}

func TestCancelWhileRunningStopsTheRunAndKeepsTheCancelledStatus(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	var ctxErr error
	h.runner.run = func(ctx context.Context, w Work, _ Output) error {
		require.NoError(t, h.cancel(w.JobID))
		<-ctx.Done()
		ctxErr = ctx.Err()
		return ctx.Err()
	}

	require.True(t, h.mustStep(t))

	require.Equal(t, context.Canceled, ctxErr)
	require.Equal(t, buildv2.JobStatus_JobStatusCancelled, h.status(t, id),
		"a cancelled job must not be overwritten with the outcome of the run that was cancelled")
	require.Empty(t, h.lastCommit(testRepo, testRef, testEnvironment))
}

func TestTheRunIsBoundedByWhicheverOfTheLeaseAndThePolicyIsShorter(t *testing.T) {
	t.Run("the lease is the bound", func(t *testing.T) {
		h := newHarnessCfg(t, func(c *Config) {
			c.BuildTimeout = time.Hour
			c.ClaimMargin = time.Minute
		})
		h.planned(t, testRef, testCommit)

		var left time.Duration
		h.runner.run = func(ctx context.Context, _ Work, _ Output) error {
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
		h := newHarnessCfg(t, func(c *Config) {
			c.BuildTimeout = time.Minute
			c.Queue.VisibilityTimeout = 24 * time.Hour
		})
		h.planned(t, testRef, testCommit)

		var left time.Duration
		h.runner.run = func(ctx context.Context, _ Work, _ Output) error {
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

func TestARunWhoseClaimExpiredMidRunPublishesNothing(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	h.runner.run = func(_ context.Context, _ Work, out Output) error {
		fmt.Fprint(out, "a build whose claim went\n")
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
	require.Equal(t, buildv2.JobStatus_JobStatusClaimed, rec.status(),
		"the loser must not write a terminal status")
	require.Empty(t, h.lastCommit(testRepo, testRef, testEnvironment), "and must not move the anchor")
	require.Zero(t, h.assets.count(),
		"and must ship nothing: an ack it cannot make is a log no record will ever name, "+
			"which is the orphan the shutdown path already declines to create")
}

func TestLeadershipLostMidRunPublishesNothing(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	h.runner.run = func(_ context.Context, _ Work, out Output) error {
		fmt.Fprint(out, "a build whose node stopped leading\n")
		h.store.stepDown()
		return nil
	}

	_, err := h.step(t.Context())
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	require.ErrorContains(t, err, "not the leader of "+build.QueueDomain)
	require.Equal(t, buildv2.JobStatus_JobStatusClaimed, h.status(t, id))
	require.Empty(t, h.lastCommit(testRepo, testRef, testEnvironment))

	_, claimed := h.work.Depth()
	require.Equal(t, 1, claimed, "and the delivery a follower could not settle comes back")
	require.Zero(t, h.assets.count(), "and nothing it built or printed reached the store")
}

func TestAWorkerThatDiedCostsAnAttemptAndTheJobComesBack(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	// A dead worker is a claim nobody ever acknowledges.
	d, err := h.work.Claim()
	require.NoError(t, err)
	require.Equal(t, uint32(1), d.Attempt)

	h.clock.advance(h.cfg.Queue.VisibilityTimeout + time.Second)

	require.True(t, h.mustStep(t))
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, h.status(t, id))
	require.Equal(t, uint32(1), h.mustRecord(t, id).Attempt,
		"the record counts the runs it actually saw")

	work := h.runner.seen()
	require.Len(t, work, 1)
	require.Equal(t, uint32(1), work[0].Attempt)
}

func TestAnExhaustedJobIsDeadLetteredAndTheDrainMarksItFailed(t *testing.T) {
	// New opens the queue from cfg, so setting MaxAttempts before construction is
	// enough.
	h := newHarnessCfg(t, func(c *Config) {
		c.Queue.MaxAttempts = 1
	})
	id := h.planned(t, testRef, testCommit)

	// One delivery nobody acknowledges, then the lease elapses.
	_, err := h.work.Claim()
	require.NoError(t, err)
	h.clock.advance(h.cfg.Queue.VisibilityTimeout + time.Second)

	// The sweep in this cycle buries it; there is nothing left to claim.
	require.False(t, h.mustStep(t))
	require.Equal(t, buildv2.JobStatus_JobStatusOpen, h.status(t, id),
		"a buried job's record is still Open until something drains the dead letter")

	dead := h.deadLetter(t)
	ready, _ := dead.Depth()
	require.Equal(t, 1, ready)

	// The next cycle drains it.
	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusFailed, rec.status())
	require.Equal(t, "attempts exhausted", rec.Reason)
	require.Empty(t, h.store.Keys(openIndexPrefix(testRepo)))

	ready, claimed := dead.Depth()
	require.Equal(t, 0, ready)
	require.Equal(t, 0, claimed)
	require.Empty(t, h.runner.seen(), "an exhausted job is never run again")
}

func TestDrainingADeadLetterEntryForAFinishedJobJustAcksIt(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	require.NoError(t, h.finishNow(t, id, buildv2.JobStatus_JobStatusCancelled, "cancelled"))

	require.NoError(t, h.dead.Push(id, nil))
	require.True(t, h.mustStep(t))

	require.Equal(t, buildv2.JobStatus_JobStatusCancelled, h.status(t, id))
	ready, _ := h.dead.Depth()
	require.Equal(t, 0, ready)
}

func TestAnItemWithNoRecordIsAckedAndDropped(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, h.work.Push(unknownJobID, nil))

	require.True(t, h.mustStep(t))
	require.Empty(t, h.runner.seen())

	ready, claimed := h.work.Depth()
	require.Equal(t, 0, ready)
	require.Equal(t, 0, claimed)
}

func TestARedeliveredJobThatIsAlreadyFinishedIsDropped(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	require.NoError(t, h.finishNow(t, id, buildv2.JobStatus_JobStatusSuccess, ""))

	require.True(t, h.mustStep(t))
	require.Empty(t, h.runner.seen())

	ready, claimed := h.work.Depth()
	require.Equal(t, 0, ready)
	require.Equal(t, 0, claimed)
}

func TestOnlyTheLeaderOfTheDomainClaims(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	follower, err := New(Config{}, WithHost(newHost(t, newMocknet(t))), WithStore(h.store), WithRepositories(h.repos),
		WithClock(h.clock.now), WithRunner(&fakeRunner{}))
	require.NoError(t, err)
	t.Cleanup(func() { follower.Close() })

	h.store.stepDown()

	_, err = follower.step(t.Context())
	require.ErrorIs(t, err, queue.ErrNotLeader)
	_, err = h.step(t.Context())
	require.ErrorIs(t, err, queue.ErrNotLeader)

	require.Equal(t, buildv2.JobStatus_JobStatusOpen, h.status(t, id))
	ready, claimed := h.work.Depth()
	require.Equal(t, 1, ready)
	require.Equal(t, 0, claimed)
}

func TestANodeWithNoRunnerKeepsTheQueueSweptAndClaimsNothing(t *testing.T) {
	store := newStore()
	clock := newClock()
	s, err := New(Config{}, WithHost(newHost(t, newMocknet(t))), WithStore(store), WithRepositories(newRepositories(testRepo)), WithClock(clock.now))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	id, err := s.enqueue(t.Context(), &buildv2.PushEvent{
		RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
	})
	require.NoError(t, err)

	worked, err := s.step(t.Context())
	require.NoError(t, err)
	require.False(t, worked)

	ready, claimed := s.work.Depth()
	require.Equal(t, 1, ready, "the item is left for a node that can run it")
	require.Equal(t, 0, claimed)

	rec, found, err := s.readRecord(id)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, buildv2.JobStatus_JobStatusOpen, rec.status())
}

func TestStepReportsAStoreThatRefusesTheClaimWrite(t *testing.T) {
	h := newHarness(t)
	h.planned(t, testRef, testCommit)

	// The record's Claimed write is the first Batch after the queue's own.
	h.store.onBatch(func() { h.store.failBatches(errors.New("log is full")) })

	_, err := h.step(t.Context())
	require.Error(t, err)
	require.Empty(t, h.runner.seen(), "a job whose claim could not be recorded must not run")
}

func TestStepReportsACorruptRecordRatherThanRunningIt(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	h.store.put(t, jobRecordKey(id), []byte("not a record"))

	_, err := h.step(t.Context())
	require.Error(t, err)
	require.Empty(t, h.runner.seen())
}

func TestTheStartedLoopEventuallyRunsAQueuedJob(t *testing.T) {
	h := newHarnessCfg(t, func(c *Config) {
		c.ClaimInterval = time.Millisecond
	})
	id := h.planned(t, testRef, testCommit)

	done := make(chan struct{})
	h.runner.run = func(context.Context, Work, Output) error { close(done); return nil }

	require.NoError(t, h.Start(t.Context()))
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the claim loop never ran the job")
	}

	require.Eventually(t, func() bool {
		return h.status(t, id) == buildv2.JobStatus_JobStatusSuccess
	}, 5*time.Second, time.Millisecond)
}

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
		h.planned(t, testRef, testCommit)

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
		h.planned(t, testRef, testCommit)
		h.store.failBatches(errors.New("log is full"))

		_, err := h.step(t.Context())
		require.ErrorContains(t, err, "claim")
		require.Empty(t, h.runner.seen())
	})
}

func TestSettleReportsARecordItCannotRead(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	h.runner.run = func(context.Context, Work, Output) error {
		h.store.put(t, jobRecordKey(id), []byte("not a record"))
		return nil
	}

	_, err := h.step(t.Context())
	require.ErrorContains(t, err, "corrupt job record")

	ready, claimed := h.work.Depth()
	require.Equal(t, 0, ready)
	require.Equal(t, 1, claimed, "a record nothing could read must not cost the item that names it")
}

func TestTheDeadLetterDrainReportsAnAckItCannotMake(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	require.NoError(t, h.finishNow(t, id, buildv2.JobStatus_JobStatusCancelled, "cancelled"))
	require.NoError(t, h.dead.Push(id, nil))

	// The claim's own entry commits, and the ack's does not.
	h.store.onBatch(func() { h.store.failBatches(errors.New("log is full")) })

	_, err := h.step(t.Context())
	require.ErrorContains(t, err, "dead letter ack")
}

func TestTheDeadLetterDrainReportsARecordItCannotWrite(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	require.NoError(t, h.dead.Push(id, nil))

	h.store.onBatch(func() { h.store.failBatches(errors.New("log is full")) })

	_, err := h.step(t.Context())
	require.ErrorContains(t, err, "finishing job "+id)

	h.store.failBatches(nil)
	ready, claimed := h.dead.Depth()
	require.Equal(t, 0, ready)
	require.Equal(t, 1, claimed, "an entry whose record could not be written stays claimed and comes back")
}

func TestASectionThatCannotBarrierDecidesNothing(t *testing.T) {
	t.Run("the claim's section", func(t *testing.T) {
		h := newHarness(t)
		id := h.planned(t, testRef, testCommit)

		// The queue's own claim commits; begin's barrier is then refused.
		h.store.onBatch(func() { h.store.failBarrier(errors.New("timed out")) })

		_, err := h.step(t.Context())
		require.ErrorContains(t, err, "barrier on "+build.QueueDomain)
		require.Empty(t, h.runner.seen(), "a job whose section could not barrier must not run")
		require.Equal(t, buildv2.JobStatus_JobStatusOpen, h.status(t, id),
			"and its record must not have moved")
	})

	t.Run("the settle's section", func(t *testing.T) {
		h := newHarness(t)
		id := h.planned(t, testRef, testCommit)

		// begin's barrier is taken and the build runs; settle's is refused.
		h.runner.run = func(context.Context, Work, Output) error {
			h.store.failBarrier(errors.New("timed out"))
			return nil
		}

		_, err := h.step(t.Context())
		require.ErrorContains(t, err, "barrier on "+build.QueueDomain)

		h.store.failBarrier(nil)
		require.Equal(t, buildv2.JobStatus_JobStatusClaimed, h.status(t, id),
			"a section that could not barrier must not decide the outcome of the run")
		require.Empty(t, h.lastCommit(testRepo, testRef, testEnvironment))
		_, claimed := h.work.Depth()
		require.Equal(t, 1, claimed, "and the delivery it did not ack comes back")
	})

	t.Run("the dead letter drain's section", func(t *testing.T) {
		h := newHarness(t)
		id := h.planned(t, testRef, testCommit)
		require.NoError(t, h.dead.Push(id, nil))

		h.store.onBatch(func() { h.store.failBarrier(errors.New("timed out")) })

		_, err := h.step(t.Context())
		require.ErrorContains(t, err, "barrier on "+build.QueueDomain)

		h.store.failBarrier(nil)
		require.Equal(t, buildv2.JobStatus_JobStatusOpen, h.status(t, id))
		ready, claimed := h.dead.Depth()
		require.Equal(t, 0, ready)
		require.Equal(t, 1, claimed, "the entry stays claimed and comes back")
	})
}

func TestTheDeadLetterDrainReportsACorruptRecord(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	require.NoError(t, h.dead.Push(id, nil))
	h.store.put(t, jobRecordKey(id), []byte("not a record"))

	_, err := h.step(t.Context())
	require.ErrorContains(t, err, "corrupt job record")

	ready, claimed := h.dead.Depth()
	require.Equal(t, 0, ready)
	require.Equal(t, 1, claimed, "and the entry is not acknowledged")
}

// meter records what was accounted for.
type meter struct {
	mu     sync.Mutex
	events []Accounted
}

func (m *meter) Record(_ context.Context, ev Accounted) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, ev)
}

func (m *meter) of(op Op) []Accounted {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []Accounted
	for _, ev := range m.events {
		if ev.Op == op {
			out = append(out, ev)
		}
	}
	return out
}

func TestASuccessfulBuildIsAccounted(t *testing.T) {
	m := &meter{}
	h := newHarness(t, WithMeter(m), WithAssets(newAssets()))
	id := h.planned(t, testRef, testCommit)

	h.runner.run = func(ctx context.Context, w Work, out Output) error {
		fmt.Fprintln(out, "building")
		h.clock.advance(90 * time.Second)
		return out.Asset(ctx, "website/home", strings.NewReader("the artifact"))
	}
	require.True(t, h.mustStep(t))

	// THE MINUTE, and what kind of minute it was: the plan's, then the build of the
	// one environment it fed.
	builds := m.of(OpBuild)
	require.Len(t, builds, 2)
	require.Empty(t, builds[0].Environment, "a plan builds no environment")
	build := builds[1]
	require.Equal(t, 90*time.Second, build.Duration, "what it occupied a builder for")
	require.True(t, build.Succeeded)
	require.Equal(t, id, build.Job)
	require.Equal(t, testRepo, build.Repository)
	require.NotEmpty(t, build.Project, "with the tenant on it")
	require.Equal(t, testEnvironment, build.Environment, "a run builds one environment")

	// THE BYTES, separately for the artifact and the log, because they have
	// different lifetimes and, in every pricing model that exists, different
	// prices.
	artifacts := m.of(OpArtifact)
	require.Len(t, artifacts, 1)
	require.Equal(t, uint64(len("the artifact")), artifacts[0].Bytes)
	require.Equal(t, "prod", artifacts[0].Environment, "and what it produced IS per-deployment")

	logs := m.of(OpLog)
	require.Len(t, logs, 1)
	require.Equal(t, uint64(len("building\n")), logs[0].Bytes)
}

func TestAFailedBuildIsAccounted(t *testing.T) {
	m := &meter{}
	h := newHarness(t, WithMeter(m), WithAssets(newAssets()))
	h.planned(t, testRef, testCommit)

	h.runner.run = func(context.Context, Work, Output) error {
		h.clock.advance(30 * time.Second)
		return errors.New("the toolchain fell over")
	}
	require.True(t, h.mustStep(t))

	builds := m.of(OpBuild)
	require.Len(t, builds, 2, "the plan's and the build's")
	require.Equal(t, 30*time.Second, builds[1].Duration)
	require.False(t, builds[1].Succeeded)
}

func TestNoMeterIsTheOrdinaryCase(t *testing.T) {
	h := newHarness(t, WithAssets(newAssets()))
	id := h.planned(t, testRef, testCommit)

	h.runner.run = func(ctx context.Context, w Work, out Output) error {
		return out.Asset(ctx, "website/home", strings.NewReader("the artifact"))
	}
	require.True(t, h.mustStep(t))
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, h.status(t, id))
}

func TestWithMeterRefusesNil(t *testing.T) {
	_, err := New(Config{}, WithHost(newHost(t, newMocknet(t))), WithStore(newStore()),
		WithRepositories(newRepositories()), WithMeter(nil))
	require.ErrorContains(t, err, "meter")
}
