package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
	"github.com/taubyte/tau/services/build/livelog"
)

// A BUILD IS WATCHABLE WHILE IT RUNS, and that is the thing v1 has no version
// of: patrick can hand you a log only once the build is over and the whole of
// it has reached IPFS.
//
// The fork is at an offset, so this is also the reconnecting client's path: the
// reader below is at 0 because it joined at the start, and one that dropped
// would come back at the offset it had.
func TestARunningBuildsLogIsFollowedWhileItIsStillBeingWritten(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	started, more := make(chan struct{}), make(chan string)
	done := make(chan error, 1)

	h.runner.run = func(_ context.Context, _ Work, out Output) error {
		fmt.Fprint(out, "cloning\n")
		close(started)
		for line := range more {
			fmt.Fprint(out, line)
		}
		return nil
	}
	go func() {
		_, err := h.step(context.WithoutCancel(t.Context()))
		done <- err
	}()

	<-started
	follower, err := h.Follow(id, 0)
	require.NoError(t, err)
	defer follower.Close()

	require.Equal(t, "cloning\n", readExactly(t, follower, 8))

	// A reader that has caught up BLOCKS rather than seeing the end of the
	// build; the next thing the build prints is what releases it.
	more <- "compiling\n"
	require.Equal(t, "compiling\n", readExactly(t, follower, 10))

	close(more)
	require.NoError(t, <-done)

	// The run is over, so the follower drains and ends — even though the file
	// itself is already gone.
	rest, err := io.ReadAll(follower)
	require.NoError(t, err)
	require.Empty(t, rest)

	require.Equal(t, buildv1.JobStatus_JobStatusSuccess, h.status(t, id))
	require.Empty(t, logFiles(t, h))

	// And it is not followable any more: what a reader wants now is the cid on
	// the job, which is an address in the asset store rather than a file on
	// whichever node happened to build it.
	_, err = h.Follow(id, 0)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// Follow is the seam a read route calls, so its refusals are the ones that
// route would return. None of them is authorization: that is the frontend's, on
// the caller's write access to the job's repository (output.go says why the
// route is not here).
func TestFollowRefusesWhatItCannotAnswer(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	_, err := h.Follow("not-a-job-id", 0)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err),
		"a caller-supplied id is refused for shape before anything is looked up")

	_, err = h.Follow(id, 0)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err),
		"a job this node is not building is a NotFound here, not a forward")

	// And an offset past the end of a log that IS being written: a client
	// resuming against an offset this log never reached is reading a different
	// attempt's, and is told so rather than handed the end.
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	h.runner.run = func(context.Context, Work, Output) error {
		close(started)
		<-release
		return nil
	}
	go func() {
		_, err := h.step(context.WithoutCancel(t.Context()))
		done <- err
	}()
	<-started

	_, err = h.Follow(id, 1<<20)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	require.ErrorContains(t, err, "offset")

	close(release)
	require.NoError(t, <-done)
}

// THE OUTPUT OF A BUILD THAT FAILED IS THE OUTPUT SOMEBODY WANTS. v1 publishes
// its log from a `defer` at the end of the run, so a build that killed its
// worker left a temp file nothing names and no cid to read it by.
func TestAFailedBuildKeepsWhatItPrintedBeforeItFailed(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	h.runner.run = func(_ context.Context, _ Work, out Output) error {
		fmt.Fprint(out, "undefined: Frobnicate\n")
		return errors.New("compile error")
	}
	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv1.JobStatus_JobStatusFailed, rec.status())
	require.Contains(t, rec.Reason, "compile error")
	require.Equal(t, "undefined: Frobnicate\n", h.assets.content(t, rec.Logs[stamp(1)]))
}

// And a runner that PANICS is the same case one step further: the recovery
// turns it into a failed build, and the output is already in hand.
func TestAPanickingBuildKeepsWhatItPrinted(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	h.runner.run = func(_ context.Context, _ Work, out Output) error {
		fmt.Fprint(out, "step 3 of 4\n")
		panic("nil map write")
	}
	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv1.JobStatus_JobStatusFailed, rec.status())
	require.Contains(t, rec.Reason, "panicked")
	require.Equal(t, "step 3 of 4\n", h.assets.content(t, rec.Logs[stamp(1)]))
}

// A CANCELLED BUILD KEEPS ITS LOG AND KEEPS ITS STATUS. The status must not
// move — a cancelled job must not be overwritten with the outcome of the run
// that was cancelled — and the log is exactly what whoever cancelled it wants
// to read. v1 carries this in a second RPC in the other direction (monkey hands
// patrick the accumulated logs so patrick can write them beside the status).
func TestACancelledBuildStillGetsItsLog(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	h.runner.run = func(ctx context.Context, w Work, out Output) error {
		fmt.Fprint(out, "installing dependencies\n")
		require.NoError(t, h.cancel(w.JobID))
		<-ctx.Done()
		return ctx.Err()
	}
	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv1.JobStatus_JobStatusCancelled, rec.status(),
		"the run's outcome must not overwrite the cancel")
	require.Equal(t, "cancelled", rec.Reason)
	require.Equal(t, "installing dependencies\n", h.assets.content(t, rec.Logs[stamp(1)]),
		"and the log of the run that was cancelled is on the record")
}

// AN ABANDONED RUN PUBLISHES NOTHING, for the same reason its record is not
// written: the delivery is left for its claim to expire and another node builds
// the job from the start. A log shipped here would be bytes in the asset store
// that no job ever names, and nothing in this design deletes those.
func TestAnAbandonedRunPublishesNothingAndLeavesNoFile(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	h.runner.run = func(ctx context.Context, _ Work, out Output) error {
		fmt.Fprint(out, "half a build\n")
		return ctx.Err()
	}

	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(errShuttingDown)

	_, err := h.step(ctx)
	require.ErrorIs(t, err, errShuttingDown)

	require.Zero(t, h.assets.count(), "nothing this run made is stored")
	require.Empty(t, h.mustRecord(t, id).Logs)
	require.Empty(t, logFiles(t, h), "and the file it wrote is not left behind either")
}

// A NODE WITH NO ASSET STORE STILL BUILDS. v2 has no storage service yet, so
// this is the shape every node has today: the log is written, it can be
// followed while the build runs, and then it goes — the job carries no cids
// because there is nowhere for the bytes to be.
func TestANodeWithNoAssetStoreBuildsAndKeepsNothing(t *testing.T) {
	h := newHarness(t, func(s *Service) error {
		s.assets = nil
		return nil
	})
	id := h.push(t, testRef, testCommit)

	var assetErr error
	h.runner.run = func(ctx context.Context, _ Work, out Output) error {
		fmt.Fprint(out, "building\n")
		assetErr = out.Asset(ctx, "website/home", strings.NewReader("the artifact"))
		return nil
	}
	require.True(t, h.mustStep(t))

	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(assetErr),
		"a runner is told there is nowhere to put an artifact rather than told it worked")

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv1.JobStatus_JobStatusSuccess, rec.status())
	require.Empty(t, rec.Logs)
	require.Empty(t, rec.Assets)
	require.Empty(t, logFiles(t, h))
}

// A LOG THAT COULD NOT BE STORED DOES NOT FAIL THE BUILD. A compile that
// succeeded did not stop succeeding because the asset store was unreachable;
// what a reader sees is a job with no log, which is the truth.
func TestAnAssetStoreThatRefusesTheLogLeavesTheJobWithoutOne(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	h.runner.run = func(ctx context.Context, _ Work, out Output) error {
		require.NoError(t, out.Asset(ctx, "website/home", strings.NewReader("the artifact")))
		fmt.Fprint(out, "built\n")
		h.assets.fail(errors.New("hoarder is down"))
		return nil
	}
	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv1.JobStatus_JobStatusSuccess, rec.status())
	require.Empty(t, rec.Logs, "the log had nowhere to go")
	require.Equal(t, map[string]string{"website/home": assetCID([]byte("the artifact"))}, rec.Assets,
		"and what DID reach the store is still named by the record")
}

// THE PUBLISH IS BOUNDED, because what follows it is the ack and the ack is
// this service's commit point. An asset store that hangs must cost the log,
// never the job.
func TestAPublishThatOutlastsItsDeadlineCostsTheLogAndNotTheJob(t *testing.T) {
	h := newHarness(t, func(s *Service) error {
		s.cfg.PublishTimeout = 10 * time.Millisecond
		return nil
	})
	id := h.push(t, testRef, testCommit)

	h.assets.block = make(chan struct{})
	defer close(h.assets.block)

	h.runner.run = func(_ context.Context, _ Work, out Output) error {
		fmt.Fprint(out, "built\n")
		return nil
	}
	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv1.JobStatus_JobStatusSuccess, rec.status())
	require.Empty(t, rec.Logs)
}

// THE CAP IS ADMISSION CONTROL ON A STRANGER'S OUTPUT — the build script that
// decides how much to print is in a repository this cloud was asked to build.
// Past it the log says so and the build carries on: a verbose build is not a
// failed one, and the runner is not told otherwise.
func TestALogPastItsCapIsTruncatedAndTheBuildStillSucceeds(t *testing.T) {
	h := newHarness(t, func(s *Service) error {
		s.cfg.MaxLogBytes = 16
		return nil
	})
	id := h.push(t, testRef, testCommit)

	var wrote int
	var writeErr error
	h.runner.run = func(_ context.Context, _ Work, out Output) error {
		out.Write([]byte("0123456789"))
		wrote, writeErr = out.Write([]byte("this is well past the cap"))
		return nil
	}
	require.True(t, h.mustStep(t))

	require.NoError(t, writeErr, "a full log must not look like a broken pipe to the build")
	require.Equal(t, len("this is well past the cap"), wrote,
		"and it must report the whole write, or an io.Copy of a build's stdout stops early")

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv1.JobStatus_JobStatusSuccess, rec.status())
	stored := h.assets.content(t, rec.Logs[stamp(1)])
	require.Contains(t, stored, "0123456789this ")
	require.Contains(t, stored, "truncated", "a truncated log that does not say so is a log that lies")
}

// A RUN THIS NODE CANNOT OPEN A LOG FOR IS A FAILED BUILD. Running an hour of
// somebody's CI and then having nothing to show for it is worse than handing
// the job to a node whose disk works.
func TestARunThatCannotOpenALogIsAFailedBuild(t *testing.T) {
	h := newHarness(t, func(s *Service) error {
		// A FILE where the directory should be: MkdirAll fails on it.
		blocked := filepath.Join(s.cfg.LogDir, "not-a-directory")
		if err := os.WriteFile(blocked, nil, 0o600); err != nil {
			return err
		}
		s.cfg.LogDir = blocked
		return nil
	})
	id := h.push(t, testRef, testCommit)

	h.runner.run = func(context.Context, Work, Output) error {
		t.Error("the run must not start without somewhere to write its log")
		return nil
	}
	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv1.JobStatus_JobStatusFailed, rec.status())
	require.Contains(t, rec.Reason, "log directory")
	require.Empty(t, rec.Logs)

	ready, claimed := h.work.Depth()
	require.Zero(t, ready)
	require.Zero(t, claimed, "and the delivery is settled rather than left to expire")
}

// An artifact needs a resource to be for and bytes to be, and both refusals are
// the runner's own fault rather than something to store.
func TestAnAssetNeedsAResourceAndContent(t *testing.T) {
	h := newHarness(t)
	h.push(t, testRef, testCommit)

	h.runner.run = func(ctx context.Context, _ Work, out Output) error {
		require.Error(t, out.Asset(ctx, "", strings.NewReader("x")))
		require.Error(t, out.Asset(ctx, "website/home", nil))
		return nil
	}
	require.True(t, h.mustStep(t))
	require.Equal(t, 1, h.assets.count(), "only the log reached the store")
}

// An asset store that answers with no cid at all is this process's own fault to
// report: a record naming the empty string is a job that says it has an
// artifact nobody can fetch.
func TestAnAssetStoreThatReturnsNoCidIsRefused(t *testing.T) {
	h := newHarness(t)
	h.push(t, testRef, testCommit)

	var err error
	h.runner.run = func(ctx context.Context, _ Work, out Output) error {
		err = (&runOutput{s: h.Service, job: "j", built: map[string]string{}}).
			Asset(ctx, "website/home", strings.NewReader("x"))
		return nil
	}
	h.assets.emptyCID = true
	require.True(t, h.mustStep(t))
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

// THE REGISTRY IS THE RUN'S, and it is empty the moment the run is over — on
// every path, because the cleanup is deferred rather than written at each exit.
func TestNothingIsLeftFollowableAfterARun(t *testing.T) {
	h := newHarness(t)

	for _, tc := range []struct {
		name   string
		commit string
		run    func(context.Context, Work, Output) error
	}{
		{"a build that succeeded", "1111111111111111111111111111111111111111",
			func(context.Context, Work, Output) error { return nil }},
		{"a build that failed", "2222222222222222222222222222222222222222",
			func(context.Context, Work, Output) error { return errors.New("compile error") }},
		{"a build that panicked", "3333333333333333333333333333333333333333",
			func(context.Context, Work, Output) error { panic("nil map write") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h.push(t, testRef, tc.commit)
			h.runner.run = tc.run
			require.True(t, h.mustStep(t))

			h.logsMu.Lock()
			defer h.logsMu.Unlock()
			require.Empty(t, h.logs, "a log left in the registry is a file nothing will close")
		})
	}
}

// AND A SECOND RUN OF ONE JOB DOES NOT UNFOLLOW THE FIRST. The queue's
// exclusion means there should not be one; if there ever is, the older run
// ending must not take the newer run's log off the registry, which is what a
// blind delete by key would do.
func TestARunEndingOnlyForgetsItsOwnLog(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	first, err := h.output(Work{JobID: id, Attempt: 1})
	require.NoError(t, err)
	second, err := h.output(Work{JobID: id, Attempt: 2})
	require.NoError(t, err)

	first.discard()

	h.logsMu.Lock()
	still := h.logs[id]
	h.logsMu.Unlock()
	require.Equal(t, second.log, still, "the newer run's log must still be followable")

	second.discard()
	h.logsMu.Lock()
	defer h.logsMu.Unlock()
	require.Empty(t, h.logs)
}

// The two runs of one job above also say why the file is named for the attempt
// and not only for the job.
func TestTwoAttemptsOfOneJobWriteDifferentFiles(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	first, err := h.output(Work{JobID: id, Attempt: 1})
	require.NoError(t, err)
	defer first.discard()
	second, err := h.output(Work{JobID: id, Attempt: 2})
	require.NoError(t, err)
	defer second.discard()

	require.NotEqual(t, first.log.Path(), second.log.Path())
	require.Len(t, logFiles(t, h), 2)
}

// discard and publish both end the log, and a run reaches both: the deferred
// cleanup runs after the publish that already closed it. Neither may be a
// second close of a file, a second delete from the registry, or a second read.
func TestEndingARunTwiceIsTheSameEnding(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	out, err := h.output(Work{JobID: id, Attempt: 1})
	require.NoError(t, err)
	fmt.Fprint(out, "one line\n")

	p := out.publish(t.Context())
	require.Equal(t, "one line\n", h.assets.content(t, p.logCID))

	out.discard()
	out.discard()
	require.Equal(t, 1, h.assets.count(), "a second ending must not store the log again")
	require.Empty(t, logFiles(t, h))
}

// A log whose file is gone before the publish is a log with no cid rather than
// a build that fails at the last step.
func TestAPublishWithNothingLeftToReadStoresNothing(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	out, err := h.output(Work{JobID: id, Attempt: 1})
	require.NoError(t, err)
	fmt.Fprint(out, "one line\n")
	require.NoError(t, out.log.Unlink())

	require.Empty(t, out.publish(t.Context()).logCID)
	require.Zero(t, h.assets.count())
}

// The writer's own refusal reaches the runner unchanged when it is not the cap:
// a build writing to a log that is already closed is a bug in this package, not
// a full log, and must not read as a successful write.
func TestAWriteToAnEndedLogIsReportedToTheRunner(t *testing.T) {
	h := newHarness(t)
	out, err := h.output(Work{JobID: h.push(t, testRef, testCommit), Attempt: 1})
	require.NoError(t, err)
	out.discard()

	n, err := out.Write([]byte("late"))
	require.ErrorIs(t, err, livelog.ErrClosed)
	require.Zero(t, n)
}

// readExactly reads n bytes off a follower, failing rather than hanging.
func readExactly(t *testing.T, r io.Reader, n int) string {
	t.Helper()

	type result struct {
		got string
		err error
	}
	done := make(chan result, 1)
	go func() {
		buf := make([]byte, n)
		read, err := io.ReadFull(r, buf)
		done <- result{string(buf[:read]), err}
	}()

	select {
	case out := <-done:
		require.NoError(t, out.err)
		return out.got
	case <-time.After(5 * time.Second):
		t.Fatal("a follower is still blocked on bytes the build has written")
		return ""
	}
}

// A log directory this node can make and a log file it cannot create are two
// different failures, and both are the same answer: the run does not start.
func TestARunThatCannotCreateItsLogFileIsAFailedBuild(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	// A DIRECTORY where the log file goes. The name is derived, so this is the
	// one way to make the create fail for every user including root.
	require.NoError(t, os.Mkdir(filepath.Join(h.cfg.LogDir, seg(id)+"."+stamp(1)+".log"), 0o700))

	h.runner.run = func(context.Context, Work, Output) error {
		t.Error("the run must not start without a log to write")
		return nil
	}
	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv1.JobStatus_JobStatusFailed, rec.status())
	require.Contains(t, rec.Reason, id, "the failure names the job it belongs to")
}

// An asset store that refuses an artifact tells the runner, which is the one
// party that can decide whether a build without it is a build at all.
func TestAnAssetStoreThatRefusesAnArtifactTellsTheRunner(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	h.assets.fail(errors.New("hoarder is down"))
	h.runner.run = func(ctx context.Context, _ Work, out Output) error {
		return out.Asset(ctx, "website/home", strings.NewReader("the artifact"))
	}
	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv1.JobStatus_JobStatusFailed, rec.status())
	require.Contains(t, rec.Reason, "hoarder is down")
	require.Empty(t, rec.Assets)
}

// A log whose file went while the build was still running is not followable,
// and that is not the caller's fault: it is an Unavailable rather than the
// InvalidArgument a bad offset gets.
func TestFollowReportsALogItCannotOpen(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	out, err := h.output(Work{JobID: id, Attempt: 1})
	require.NoError(t, err)
	defer out.discard()
	require.NoError(t, out.log.Unlink())

	_, err = h.Follow(id, 0)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
}

// A run whose record disappeared underneath it settles the delivery and writes
// nothing: there is no record to put an outcome on, and the log it produced has
// nothing to be named by.
func TestSettleWritesNothingForARecordThatIsGone(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	h.runner.run = func(_ context.Context, _ Work, out Output) error {
		fmt.Fprint(out, "built\n")
		require.NoError(t, h.store.MockCluster.Delete(jobRecordKey(id), time.Second))
		return nil
	}
	require.True(t, h.mustStep(t))

	_, found, err := h.readRecord(id)
	require.NoError(t, err)
	require.False(t, found)

	ready, claimed := h.work.Depth()
	require.Zero(t, ready)
	require.Zero(t, claimed, "the delivery is still acknowledged")
}

// A RUN THAT PRODUCED NOTHING WRITES NOTHING ONTO A TERMINAL RECORD. A
// replicated entry that changes no field is a cost with no fact in it, and this
// is the path every cancelled job takes on a node with no asset store.
func TestACancelledJobWithNoOutputCostsNoEntry(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.finishNow(t, id, buildv1.JobStatus_JobStatusCancelled, "cancelled"))

	before := h.store.appliedEntries()
	require.NoError(t, h.settle(d, produced{}, nil))
	require.Equal(t, before+1, h.store.appliedEntries(),
		"the ack is the one write: nothing produced is nothing to record")
}

// A LOG IS FILED UNDER THE ATTEMPT THAT WROTE IT, not under whatever the record
// says when the build is over. The record is re-read after the run, outside the
// lock the run did not hold, and the two numbers can differ — settle's own doc
// lists three things that can have written it in the meantime.
func TestALogIsFiledUnderTheRunsAttemptAndNotTheRecordsAtSettleTime(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	d, err := h.work.Claim()
	require.NoError(t, err)

	rec := h.mustRecord(t, id)
	rec.Status = int32(buildv1.JobStatus_JobStatusClaimed)
	rec.Attempt = 7
	value, err := rec.encode()
	require.NoError(t, err)
	h.store.put(t, jobRecordKey(id), value)

	require.NoError(t, h.settle(d, produced{attempt: 3, logCID: "cid-of-run-3"}, nil))

	require.Equal(t, map[string]string{stamp(3): "cid-of-run-3"}, h.mustRecord(t, id).Logs)
}

// AND RECORDING IT ON A TERMINAL JOB MOVES NOTHING ELSE. The status, the reason
// and the instant the job finished are all left where the cancel put them —
// which matters beyond tidiness, because the retention row is keyed by that
// instant, and a record whose timestamp moved is a job listed as retained at a
// second nothing wrote.
func TestRecordingALogOnACancelledJobMovesNothingElse(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.finishNow(t, id, buildv1.JobStatus_JobStatusCancelled, "cancelled"))

	before := h.mustRecord(t, id)
	h.clock.advance(time.Hour)

	require.NoError(t, h.settle(d, produced{attempt: 1, logCID: "cid-x"}, nil))

	after := h.mustRecord(t, id)
	require.Equal(t, before.Status, after.Status)
	require.Equal(t, before.Reason, after.Reason)
	require.Equal(t, before.UpdatedAt, after.UpdatedAt, "the clock moved and the record must not")
	require.Equal(t, map[string]string{stamp(1): "cid-x"}, after.Logs)

	_, retained := h.store.Get(terminalIndexKey(time.Unix(after.UpdatedAt, 0), id))
	require.True(t, retained, "the retention row still names the instant the record carries")
}
