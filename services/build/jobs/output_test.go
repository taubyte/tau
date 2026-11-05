package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	runnerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/runner/v2"
	"github.com/taubyte/tau/services/build/run"
)

// What a run writes is its runner's to keep (services/runner); these are what
// becomes of it on the job: the cids a record names, and when it names none.

func TestAFailedBuildKeepsWhatItPrintedBeforeItFailed(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	h.runner.run = func(_ context.Context, _ run.Work, out run.Output) error {
		fmt.Fprint(out, "undefined: Frobnicate\n")
		return errors.New("compile error")
	}
	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusFailed, rec.status())
	require.Contains(t, rec.Reason, "compile error")
	require.Equal(t, "undefined: Frobnicate\nerror: compile error\n", h.assets.content(t, rec.Logs[stamp(1)]),
		"what it printed, then why it failed: the record carries no reason anybody can read")
	require.Empty(t, logFiles(t, h), "a sealed log is not left on the runner")
}

func TestAPanickingBuildKeepsWhatItPrinted(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	h.runner.run = func(_ context.Context, _ run.Work, out run.Output) error {
		fmt.Fprint(out, "step 3 of 4\n")
		panic("nil map write")
	}
	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusFailed, rec.status())
	require.Contains(t, rec.Reason, "panicked")
	require.Equal(t, "step 3 of 4\nerror: runner panicked: nil map write\n", h.assets.content(t, rec.Logs[stamp(1)]))
}

func TestACancelledBuildStillGetsItsLog(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	h.runner.run = func(ctx context.Context, w run.Work, out run.Output) error {
		fmt.Fprint(out, "installing dependencies\n")
		require.NoError(t, h.cancel(w.JobID))
		<-ctx.Done()
		return ctx.Err()
	}
	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusCancelled, rec.status(),
		"the run's outcome must not overwrite the cancel")
	require.Equal(t, "cancelled", rec.Reason)
	require.Equal(t, "installing dependencies\nerror: context canceled\n", h.assets.content(t, rec.Logs[stamp(1)]),
		"and the log of the run that was cancelled is on the record")
}

func TestAnAbandonedRunKeepsNothing(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	h.runner.run = func(ctx context.Context, _ run.Work, out run.Output) error {
		fmt.Fprint(out, "half a build\n")
		return ctx.Err()
	}

	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(errShuttingDown)
	d, err := h.work.Claim()
	require.NoError(t, err)
	require.ErrorIs(t, h.run(ctx, d, h.runnerID, h.onRunner), errShuttingDown,
		"a runner in this process still answers, and the shutdown is what decides")

	require.Zero(t, h.assets.count(), "nothing this run made is stored")
	require.Empty(t, h.mustRecord(t, id).Logs)
	require.Empty(t, logFiles(t, h), "and its log is let go on the runner")
	require.Equal(t, buildv2.JobStatus_JobStatusClaimed, h.status(t, id), "abandoned: its claim lapses and the queue hands it on")
}

func TestARunnerThatTurnsTheRunDownCostsTheRun(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	require.NoError(t, os.RemoveAll(h.logDir))
	require.NoError(t, os.WriteFile(h.logDir, nil, 0o600), "a file where the runner's log directory goes")

	h.runner.run = func(context.Context, run.Work, run.Output) error {
		t.Error("the run must not start without somewhere to write its log")
		return nil
	}
	err := h.runNext(t)
	require.ErrorIs(t, err, errRunnerLost)
	require.ErrorContains(t, err, "log directory")
	require.Equal(t, buildv2.JobStatus_JobStatusClaimed, h.status(t, id), "the job is handed on, not failed")
}

func TestARunnerWithNowhereToKeepAnythingBuildsAndKeepsNothing(t *testing.T) {
	h := newHarnessKeeping(t, nil)
	id := h.planned(t, testRef, testCommit)

	var assetErr error
	h.runner.run = func(ctx context.Context, _ run.Work, out run.Output) error {
		fmt.Fprint(out, "building\n")
		assetErr = out.Asset(ctx, "website/home", strings.NewReader("the artifact"))
		return nil
	}
	require.True(t, h.mustStep(t))

	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(assetErr),
		"a build is told there is nowhere to put an artifact rather than told it worked")

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, rec.status())
	require.Empty(t, rec.Logs)
	require.Empty(t, rec.Assets)
	require.Empty(t, logFiles(t, h))
}

func TestAnAssetStoreThatRefusesTheLogLeavesTheJobWithoutOne(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	h.runner.run = func(ctx context.Context, _ run.Work, out run.Output) error {
		require.NoError(t, out.Asset(ctx, "website/home", strings.NewReader("the artifact")))
		fmt.Fprint(out, "built\n")
		h.assets.fail(errors.New("storage is down"))
		return nil
	}
	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, rec.status())
	require.Empty(t, rec.Logs, "the log had nowhere to go")
	require.Equal(t, map[string]string{"prod/website/home": assetCID([]byte("the artifact"))}, rec.Assets,
		"and what DID reach the store is still named by the record")
}

func TestASealThatOutlastsItsDeadlineCostsTheLogAndNotTheJob(t *testing.T) {
	h := newHarnessCfg(t, func(c *Config) {
		c.PublishTimeout = 10 * time.Millisecond
	})
	id := h.planned(t, testRef, testCommit)
	h.runners = oneRunner{id: h.runnerID, runs: slowSeal{Service: h.onRunner}}

	require.ErrorContains(t, h.runNext(t), "sealing its log")
	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, rec.status())
	require.Empty(t, rec.Logs)
}

// slowSeal is a runner whose seal answers only once the caller gives up.
type slowSeal struct{ run.Service }

func (slowSeal) Seal(ctx context.Context, _ *runnerv2.Sealing) (*runnerv2.Sealed, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestALogTheRunnerCannotHandOverWholeIsNamedByNothing(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	h.runner.run = func(context.Context, run.Work, run.Output) error {
		files, err := filepath.Glob(filepath.Join(h.logDir, "*.log"))
		if err != nil || len(files) != 1 {
			return errors.New("no log file")
		}
		// Not the runner's doing: the file is shortened under the run.
		return os.Truncate(files[0], 0)
	}
	require.ErrorContains(t, h.runNext(t), "sealing its log")

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, rec.status(),
		"the job settled before its log was kept, and does not hang on it")
	require.Empty(t, rec.Logs, "and nothing names a short log")
}

func TestAnAssetStoreThatRefusesAnArtifactFailsTheBuild(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	h.assets.fail(errors.New("storage is down"))
	h.runner.run = func(ctx context.Context, _ run.Work, out run.Output) error {
		return out.Asset(ctx, "website/home", strings.NewReader("the artifact"))
	}
	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusFailed, rec.status())
	require.Contains(t, rec.Reason, "storage is down")
	require.Empty(t, rec.Assets)
}

func TestAStoredArtifactWithoutItsNamesFailsTheBuild(t *testing.T) {
	h := newHarness(t)
	rec := jobRecord{ID: jobIDOf(t, testCommit), Environment: testEnvironment, Attempt: 1}
	_, err := h.produced(t.Context(), rec, []*runnerv2.Stored{{ResourceId: "website/home"}})
	require.ErrorContains(t, err, "names its resource and its cid")
}

func TestNothingIsLeftOnTheRunnerAfterARun(t *testing.T) {
	h := newHarness(t)

	type testCase struct {
		name   string
		commit string
		run    func(context.Context, run.Work, run.Output) error
	}
	for _, tc := range []testCase{
		{"a build that succeeded", "1111111111111111111111111111111111111111",
			func(context.Context, run.Work, run.Output) error { return nil }},
		{"a build that failed", "2222222222222222222222222222222222222222",
			func(context.Context, run.Work, run.Output) error { return errors.New("compile error") }},
		{"a build that panicked", "3333333333333333333333333333333333333333",
			func(context.Context, run.Work, run.Output) error { panic("nil map write") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h.planned(t, testRef, tc.commit)
			h.runner.run = tc.run
			require.True(t, h.mustStep(t))
			require.Empty(t, logFiles(t, h), "a log left on the runner is a file nothing will close")
		})
	}
}

func TestSettleKeepsNothingForARecordThatIsGone(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	h.runner.run = func(_ context.Context, _ run.Work, out run.Output) error {
		fmt.Fprint(out, "built\n")
		h.records.erase(t, id)
		return nil
	}
	require.True(t, h.mustStep(t))

	_, found, err := h.readRecord(id)
	require.NoError(t, err)
	require.False(t, found)

	ready, claimed := h.work.Depth()
	require.Zero(t, ready)
	require.Zero(t, claimed, "the delivery is still acknowledged")
	require.Zero(t, h.assets.count(), "there is no record to name a log, so the log is not kept")
	require.Empty(t, logFiles(t, h))
}

// ranOnTheRunner is a run of id at attempt, ended on the harness's runner and left
// for its seal, having printed said.
func ranOnTheRunner(t *testing.T, h *harness, id string, attempt uint32, said string) jobRecord {
	t.Helper()
	h.runner.run = func(_ context.Context, _ run.Work, out run.Output) error {
		fmt.Fprint(out, said)
		return nil
	}
	_, err := h.onRunner.Build(t.Context(), run.Work{JobID: id, Attempt: attempt, Environment: testEnvironment, ProjectID: "shop"})
	require.NoError(t, err)
	return jobRecord{ID: id, Attempt: attempt, Environment: testEnvironment, ProjectID: "shop"}
}

func TestACancelledJobWithNoOutputCostsNoEntry(t *testing.T) {
	h := newHarnessKeeping(t, nil)
	id := h.planned(t, testRef, testCommit)

	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.finishNow(t, id, buildv2.JobStatus_JobStatusCancelled, "cancelled"))
	ran := ranOnTheRunner(t, h, id, 1, "nowhere to put this\n")

	before := h.records.appliedEntries()
	require.NoError(t, h.settle(t.Context(), d, h.onRunner, ran, produced{attempt: 1}, nil))
	require.Equal(t, before, h.records.appliedEntries(),
		"the ack is the one write, and it is the queue's: nothing produced is nothing to record")
}

func TestALogIsFiledUnderTheRunsAttemptAndNotTheRecordsAtSettleTime(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	d, err := h.work.Claim()
	require.NoError(t, err)

	rec := h.mustRecord(t, id)
	rec.Status = int32(buildv2.JobStatus_JobStatusClaimed)
	rec.Attempt = 7
	h.plantRecord(t, rec)

	ran := ranOnTheRunner(t, h, id, 3, "what run three printed\n")
	require.NoError(t, h.settle(t.Context(), d, h.onRunner, ran, produced{attempt: 3}, nil))

	require.Equal(t, map[string]string{stamp(3): assetCID([]byte("what run three printed\n"))},
		h.mustRecord(t, id).Logs)
}

func TestRecordingALogOnACancelledJobMovesNothingElse(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.finishNow(t, id, buildv2.JobStatus_JobStatusCancelled, "cancelled"))
	ran := ranOnTheRunner(t, h, id, 1, "cancelled midway\n")

	before := h.mustRecord(t, id)
	h.clock.advance(time.Hour)

	require.NoError(t, h.settle(t.Context(), d, h.onRunner, ran, produced{attempt: 1}, nil))

	after := h.mustRecord(t, id)
	require.Equal(t, before.Status, after.Status)
	require.Equal(t, before.Reason, after.Reason)
	require.Equal(t, before.UpdatedAt, after.UpdatedAt, "the clock moved and the record must not")
	require.Equal(t, map[string]string{stamp(1): assetCID([]byte("cancelled midway\n"))}, after.Logs)

	require.Contains(t, h.terminal(t, id), terminalRow(time.Unix(after.UpdatedAt, 0), id),
		"the retention row still names the instant the job carries")
}

func TestASlowAssetStoreDoesNotStallEveryOtherJobOnTheNode(t *testing.T) {
	const elsewhere = "github.com/acme/unrelated"

	h := newHarness(t)
	h.repos.add(elsewhere)
	id := h.planned(t, testRef, testCommit)

	storing := make(chan struct{})
	release := make(chan struct{})
	h.assets.block = release
	h.assets.onPut = func() { close(storing) }

	h.runner.run = func(_ context.Context, _ run.Work, out run.Output) error {
		fmt.Fprint(out, "a build whose log is going to a store that has stopped\n")
		return nil
	}

	settled := make(chan error, 1)
	go func() { _, err := h.step(context.Background()); settled <- err }()
	<-storing

	enqueued := make(chan error, 1)
	go func() {
		_, err := h.enqueue(context.Background(), &buildv2.PushEvent{
			RepositoryKey: elsewhere, Ref: testRef, CommitSha: testCommit,
		})
		enqueued <- err
	}()

	select {
	case err := <-enqueued:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("a push for another repository is waiting on this node's asset store")
	}

	close(release)
	require.NoError(t, <-settled)
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, h.status(t, id))
}

func TestALogCanReachTheStoreAndNoCidReachAnyJob(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	// ONCE: the flap is this run's.
	h.assets.onStored = sync.OnceFunc(func() { h.store.stepDown() })
	h.runner.run = func(_ context.Context, w run.Work, out run.Output) error {
		fmt.Fprintf(out, "attempt %d printed this\n", w.Attempt)
		return nil
	}
	orphaned := assetCID([]byte("attempt 1 printed this\n"))

	err := h.runNext(t)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))

	h.store.stepUp()

	require.Equal(t, 1, h.assets.objects(), "the store kept the log")
	require.Equal(t, "attempt 1 printed this\n", h.assets.content(t, orphaned))

	// THE JOB SETTLED FIRST, so it is done; the log it ends with is named by nothing,
	// with nothing in this service able to unreference it.
	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, rec.status())
	require.Empty(t, rec.Logs, "and no job names it")

	ready, claimed := h.work.Depth()
	require.Zero(t, ready)
	require.Zero(t, claimed, "the ack landed, so there is no delivery left either")
}

func TestAJobReopenedWhileItsLogWasStoredKeepsThatLog(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	storing := make(chan struct{})
	release := make(chan struct{})
	h.assets.block = release
	h.assets.onPut = func() { close(storing) }

	h.runner.run = func(_ context.Context, _ run.Work, out run.Output) error {
		fmt.Fprint(out, "what the cancelled attempt printed\n")
		return nil
	}

	stepped := make(chan error, 1)
	go func() { _, err := h.step(context.Background()); stepped <- err }()
	<-storing

	// THE JOB SETTLED BEFORE ITS LOG WENT, so a cancel is too late and a retry is
	// not: it takes jobLock, which the seal is not holding.
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, h.status(t, id))
	require.NoError(t, h.retry(id))
	close(release)
	require.NoError(t, <-stepped)

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusOpen, rec.status(),
		"the run that ended does not get to write over the attempt that replaced it")
	require.Equal(t,
		map[string]string{stamp(1): assetCID([]byte("what the cancelled attempt printed\n"))},
		rec.Logs, "and what it printed is still readable, under its own attempt")
	require.Equal(t, 1, h.assets.objects())

	ready, _ := h.work.Depth()
	require.Equal(t, 1, ready, "the retry's item is there, and the requeue deduplicated against it")
}

func TestARecordThatWentWhileItsLogWasStoredIsNotWrittenBack(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	storing := make(chan struct{})
	release := make(chan struct{})
	h.assets.block = release
	h.assets.onPut = func() { close(storing) }
	h.runner.run = func(_ context.Context, _ run.Work, out run.Output) error {
		fmt.Fprint(out, "built\n")
		return nil
	}

	stepped := make(chan error, 1)
	go func() { _, err := h.step(context.Background()); stepped <- err }()
	<-storing

	h.records.erase(t, id)
	close(release)
	require.NoError(t, <-stepped)

	_, found, err := h.readRecord(id)
	require.NoError(t, err)
	require.False(t, found, "a record nobody put back is a record that stayed gone")
	require.Equal(t, 1, h.assets.objects(), "and the log the store already took stays with it")
}

func TestAReopenedJobsLogReportsAStoreThatRefusedIt(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	storing := make(chan struct{})
	release := make(chan struct{})
	h.assets.block = release
	h.assets.onPut = func() { close(storing) }
	h.runner.run = func(_ context.Context, _ run.Work, out run.Output) error {
		fmt.Fprint(out, "built\n")
		return nil
	}

	stepped := make(chan error, 1)
	go func() { stepped <- h.runNext(t) }()
	<-storing

	require.NoError(t, h.retry(id))
	h.records.failBatches(errors.New("the log is written and the store said no"))
	close(release)

	err := <-stepped
	require.ErrorContains(t, err, "the store said no")
	require.ErrorContains(t, err, "recording the output of")
}
