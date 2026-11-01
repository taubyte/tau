package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/services/build/livelog"
)

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
	follower, err := h.Follow(id, 1, 0)
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

	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, h.status(t, id))
	require.Empty(t, logFiles(t, h))

	// And it is no longer followable: the cid on the job is the address now.
	_, err = h.Follow(id, 1, 0)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestFollowRefusesWhatItCannotAnswer(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	_, err := h.Follow("not-a-job-id", 1, 0)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err),
		"a caller-supplied id is refused for shape before anything is looked up")

	_, err = h.Follow(id, 1, 0)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err),
		"a job this node is not building is a NotFound here, not a forward")

	// And an offset past the end of a log that IS being written: the caller's
	// arithmetic, told so rather than handed the end.
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

	_, err = h.Follow(id, 1, 1<<20)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	require.ErrorContains(t, err, "offset")

	close(release)
	require.NoError(t, <-done)
}

func TestAFailedBuildKeepsWhatItPrintedBeforeItFailed(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	h.runner.run = func(_ context.Context, _ Work, out Output) error {
		fmt.Fprint(out, "undefined: Frobnicate\n")
		return errors.New("compile error")
	}
	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusFailed, rec.status())
	require.Contains(t, rec.Reason, "compile error")
	require.Equal(t, "undefined: Frobnicate\nerror: compile error\n", h.assets.content(t, rec.Logs[stamp(1)]),
		"what it printed, then why it failed: the record carries no reason anybody can read")
}

func TestAPanickingBuildKeepsWhatItPrinted(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	h.runner.run = func(_ context.Context, _ Work, out Output) error {
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
	id := h.push(t, testRef, testCommit)

	h.runner.run = func(ctx context.Context, w Work, out Output) error {
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

func TestANodeWithNoAssetStoreBuildsAndKeepsNothing(t *testing.T) {
	h := newHarness(t, withoutAssets())
	id := h.push(t, testRef, testCommit)

	var assetErr error
	h.runner.run = func(ctx context.Context, _ Work, out Output) error {
		fmt.Fprint(out, "building\n")
		assetErr = out.Asset(ctx, "prod", "website/home", strings.NewReader("the artifact"))
		return nil
	}
	require.True(t, h.mustStep(t))

	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(assetErr),
		"a runner is told there is nowhere to put an artifact rather than told it worked")

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, rec.status())
	require.Empty(t, rec.Logs)
	require.Empty(t, rec.Assets)
	require.Empty(t, logFiles(t, h))
}

func TestAnAssetStoreThatRefusesTheLogLeavesTheJobWithoutOne(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	h.runner.run = func(ctx context.Context, _ Work, out Output) error {
		require.NoError(t, out.Asset(ctx, "prod", "website/home", strings.NewReader("the artifact")))
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

func TestAPublishThatOutlastsItsDeadlineCostsTheLogAndNotTheJob(t *testing.T) {
	h := newHarnessCfg(t, func(c *Config) {
		c.PublishTimeout = 10 * time.Millisecond
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
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, rec.status())
	require.Empty(t, rec.Logs)
}

func TestALogPastItsCapIsTruncatedAndTheBuildStillSucceeds(t *testing.T) {
	h := newHarnessCfg(t, func(c *Config) {
		c.MaxLogBytes = 16
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
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, rec.status())
	stored := h.assets.content(t, rec.Logs[stamp(1)])
	require.Contains(t, stored, "0123456789this ")
	require.Contains(t, stored, "truncated", "a truncated log that does not say so is a log that lies")
}

func TestARunThatCannotOpenALogIsAFailedBuild(t *testing.T) {
	h := newHarnessCfg(t, func(c *Config) {
		// A FILE where the directory should be: MkdirAll fails on it.
		blocked := filepath.Join(c.LogDir, "not-a-directory")
		require.NoError(t, os.WriteFile(blocked, nil, 0o600))
		c.LogDir = blocked
	})
	id := h.push(t, testRef, testCommit)

	h.runner.run = func(context.Context, Work, Output) error {
		t.Error("the run must not start without somewhere to write its log")
		return nil
	}
	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusFailed, rec.status())
	require.Contains(t, rec.Reason, "log directory")
	require.Empty(t, rec.Logs)

	ready, claimed := h.work.Depth()
	require.Zero(t, ready)
	require.Zero(t, claimed, "and the delivery is settled rather than left to expire")
}

func TestAnAssetNeedsAResourceAndContent(t *testing.T) {
	h := newHarness(t)
	h.push(t, testRef, testCommit)

	h.runner.run = func(ctx context.Context, _ Work, out Output) error {
		require.Error(t, out.Asset(ctx, "prod", "", strings.NewReader("x")))
		require.Error(t, out.Asset(ctx, "prod", "website/home", nil))
		return nil
	}
	require.True(t, h.mustStep(t))
	require.Equal(t, 1, h.assets.count(), "only the log reached the store")
}

func TestAnAssetStoreThatReturnsNoCidIsRefused(t *testing.T) {
	h := newHarness(t)
	h.push(t, testRef, testCommit)

	var err error
	h.runner.run = func(ctx context.Context, _ Work, out Output) error {
		err = (&runOutput{s: h.Service, job: "j", built: map[assetRef]string{}}).
			Asset(ctx, "prod", "website/home", strings.NewReader("x"))
		return nil
	}
	h.assets.emptyCID = true
	require.True(t, h.mustStep(t))
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

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

func TestARunEndingOnlyForgetsItsOwnLog(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	first, err := h.output(Work{JobID: id, Attempt: 1})
	require.NoError(t, err)
	second, err := h.output(Work{JobID: id, Attempt: 2})
	require.NoError(t, err)

	first.discard()

	h.logsMu.Lock()
	still := h.logs[id].log
	h.logsMu.Unlock()
	require.Equal(t, second.log, still, "the newer run's log must still be followable")

	second.discard()
	h.logsMu.Lock()
	defer h.logsMu.Unlock()
	require.Empty(t, h.logs)
}

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

func TestEndingARunTwiceIsTheSameEnding(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	out, err := h.output(Work{JobID: id, Attempt: 1})
	require.NoError(t, err)
	fmt.Fprint(out, "one line\n")

	p, err := out.publish(t.Context())
	require.NoError(t, err)
	require.Equal(t, "one line\n", h.assets.content(t, p.logCID))

	out.discard()
	out.discard()
	require.Equal(t, 1, h.assets.count(), "a second ending must not store the log again")
	require.Empty(t, logFiles(t, h))
}

func TestALogWhoseNameWentIsPublishedAnyway(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	out, err := h.output(Work{JobID: id, Attempt: 1})
	require.NoError(t, err)
	fmt.Fprint(out, "one line\n")

	// Not out.log.Unlink(): somebody ELSE'S removal of the name, which is what
	// a tmp sweep is.
	require.NoError(t, os.Remove(out.log.Path()))

	p, err := out.publish(t.Context())
	require.NoError(t, err)
	require.Equal(t, "one line\n", h.assets.content(t, p.logCID))
}

func TestAPublishThatCannotReadItsLogSaysSo(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	out, err := h.output(Work{JobID: id, Attempt: 1})
	require.NoError(t, err)
	fmt.Fprint(out, "one line\n")

	// Unlink is the log's own end — the name goes AND this log lets go of the
	// inode — so there is nothing left to read back.
	require.NoError(t, out.log.Unlink())

	p, err := out.publish(t.Context())
	require.ErrorContains(t, err, "reading back the log it wrote")
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
	require.Empty(t, p.logCID)
	require.Zero(t, h.assets.count(), "and nothing half-read reaches the store")
}

func TestALogThatLostBytesFailsTheBuildRatherThanBeingStoredShort(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	h.runner.run = func(_ context.Context, w Work, out Output) error {
		fmt.Fprint(out, "cloning\n")
		// Not this package's doing: the file is shortened under the run, which
		// is what the log's own size measurement is against.
		return os.Truncate(logFile(h, w), 0)
	}
	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusFailed, rec.status(),
		"a log this node cannot read back is a failed build, not a build with no log")
	require.Contains(t, rec.Reason, "reading back the log it wrote")
	require.Empty(t, rec.Logs, "and nothing names a short log")
	require.Zero(t, h.assets.objects(), "which the store did not take either")
}

func TestAStoreThatSwallowedAReadErrorDoesNotGetToNameTheLog(t *testing.T) {
	h := newHarness(t, WithAssets(&swallowingStore{}))
	id := h.push(t, testRef, testCommit)

	h.runner.run = func(_ context.Context, w Work, out Output) error {
		fmt.Fprint(out, "cloning\n")
		return os.Truncate(logFile(h, w), 0)
	}
	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusFailed, rec.status())
	require.Contains(t, rec.Reason, "reading back the log it wrote")
	require.Empty(t, rec.Logs, "the cid it answered with names bytes that are not the log")
}

func TestAStoreThatStoppedReadingDoesNotGetToNameTheLog(t *testing.T) {
	h := newHarness(t, WithAssets(&shortStore{}))
	id := h.push(t, testRef, testCommit)

	h.runner.run = func(_ context.Context, _ Work, out Output) error {
		fmt.Fprint(out, "cloning\ncompiling\nlinking\n")
		return nil
	}
	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusFailed, rec.status())
	require.Contains(t, rec.Reason, "took 4 of the 26 bytes")
	require.Empty(t, rec.Logs, "a cid over a prefix of the log is not this log")
}

// shortStore reads four bytes, stops, and answers with a cid — a store that
// broke the half of its contract an io.Reader cannot state.
type shortStore struct{}

func (*shortStore) Put(_ context.Context, content io.Reader) (string, error) {
	raw := make([]byte, 4)
	n, _ := content.Read(raw)
	return assetCID(raw[:n]), nil
}

// swallowingStore is an asset store that reads what it is handed, ignores a
// read that failed, and answers with a cid anyway. It is the store the check
// above exists for and the one an interface cannot forbid.
type swallowingStore struct{}

func (*swallowingStore) Put(_ context.Context, content io.Reader) (string, error) {
	raw, _ := io.ReadAll(content)
	return assetCID(raw), nil
}

// logFile is where a run's log is, named the way output() names it.
func logFile(h *harness, w Work) string {
	return filepath.Join(h.cfg.LogDir, seg(w.JobID)+"."+stamp(int64(w.Attempt))+".log")
}

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
	require.Equal(t, buildv2.JobStatus_JobStatusFailed, rec.status())
	require.Contains(t, rec.Reason, id, "the failure names the job it belongs to")
}

func TestAnAssetStoreThatRefusesAnArtifactTellsTheRunner(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	h.assets.fail(errors.New("storage is down"))
	h.runner.run = func(ctx context.Context, _ Work, out Output) error {
		return out.Asset(ctx, "prod", "website/home", strings.NewReader("the artifact"))
	}
	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusFailed, rec.status())
	require.Contains(t, rec.Reason, "storage is down")
	require.Empty(t, rec.Assets)
}

func TestFollowReportsALogItCannotOpen(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	out, err := h.output(Work{JobID: id, Attempt: 1})
	require.NoError(t, err)
	defer out.discard()
	require.NoError(t, out.log.Unlink())

	_, err = h.Follow(id, 1, 0)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
}

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

	// It ships nothing either: the publish is asked for only after a section found a
	// record for the cid.
	require.Zero(t, h.assets.count(),
		"there is no record to name a log, so the log does not go")
}

func TestACancelledJobWithNoOutputCostsNoEntry(t *testing.T) {
	h := newHarness(t, withoutAssets())
	id := h.push(t, testRef, testCommit)

	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.finishNow(t, id, buildv2.JobStatus_JobStatusCancelled, "cancelled"))

	out, err := h.output(Work{JobID: id, Attempt: 1})
	require.NoError(t, err)
	defer out.discard()
	fmt.Fprint(out, "nowhere to put this\n")

	before := h.store.appliedEntries()
	require.NoError(t, h.settle(t.Context(), d, out, nil))
	require.Equal(t, before+1, h.store.appliedEntries(),
		"the ack is the one write: nothing produced is nothing to record")
}

func TestALogIsFiledUnderTheRunsAttemptAndNotTheRecordsAtSettleTime(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	d, err := h.work.Claim()
	require.NoError(t, err)

	rec := h.mustRecord(t, id)
	rec.Status = int32(buildv2.JobStatus_JobStatusClaimed)
	rec.Attempt = 7
	value, err := rec.encode()
	require.NoError(t, err)
	h.store.put(t, jobRecordKey(id), value)

	out, err := h.output(Work{JobID: id, Attempt: 3})
	require.NoError(t, err)
	defer out.discard()
	fmt.Fprint(out, "what run three printed\n")

	require.NoError(t, h.settle(t.Context(), d, out, nil))

	require.Equal(t, map[string]string{stamp(3): assetCID([]byte("what run three printed\n"))},
		h.mustRecord(t, id).Logs)
}

func TestRecordingALogOnACancelledJobMovesNothingElse(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.finishNow(t, id, buildv2.JobStatus_JobStatusCancelled, "cancelled"))

	out, err := h.output(Work{JobID: id, Attempt: 1})
	require.NoError(t, err)
	defer out.discard()
	fmt.Fprint(out, "cancelled midway\n")

	before := h.mustRecord(t, id)
	h.clock.advance(time.Hour)

	require.NoError(t, h.settle(t.Context(), d, out, nil))

	after := h.mustRecord(t, id)
	require.Equal(t, before.Status, after.Status)
	require.Equal(t, before.Reason, after.Reason)
	require.Equal(t, before.UpdatedAt, after.UpdatedAt, "the clock moved and the record must not")
	require.Equal(t, map[string]string{stamp(1): assetCID([]byte("cancelled midway\n"))}, after.Logs)

	_, retained := h.store.Get(terminalIndexKey(time.Unix(after.UpdatedAt, 0), id))
	require.True(t, retained, "the retention row still names the instant the record carries")
}

func TestALogIsNeverOpenedThroughASymlinkPlantedAtItsPath(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	victim := filepath.Join(t.TempDir(), "authorized_keys")
	const secret = "ssh-ed25519 AAAA-the-operators-own-key\n"
	require.NoError(t, os.WriteFile(victim, []byte(secret), 0o600))

	planted := logFile(h, Work{JobID: id, Attempt: 1})
	require.NoError(t, os.Symlink(victim, planted))

	h.runner.run = func(context.Context, Work, Output) error {
		t.Error("the run must not start against a log this node did not create")
		return nil
	}
	require.True(t, h.mustStep(t))

	kept, err := os.ReadFile(victim)
	require.NoError(t, err)
	require.Equal(t, secret, string(kept), "the file the link pointed at was written through")

	link, err := os.Lstat(planted)
	require.NoError(t, err, "and the link was removed, which is how the trace goes")
	require.NotZero(t, link.Mode()&os.ModeSymlink)

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusFailed, rec.status(),
		"a node that cannot open a log of its own is not the node that runs the build")
	require.Contains(t, rec.Reason, id)
}

func TestTheLogDirectoryTheServiceUsesIsOneNobodyElseCanWriteIn(t *testing.T) {
	shared := filepath.Join(t.TempDir(), "tau-build-logs")
	require.NoError(t, os.Mkdir(shared, 0o777))

	h := newHarnessCfg(t, func(c *Config) {
		c.LogDir = shared
	})
	id := h.push(t, testRef, testCommit)
	require.True(t, h.mustStep(t))
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, h.status(t, id))

	fi, err := os.Lstat(shared)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), fi.Mode().Perm(),
		"a directory an operator pointed at something shared is tightened before a run writes in it")
}

func TestTheDefaultLogDirectoryIsThisAccountsAlone(t *testing.T) {
	cfg, err := Config{}.withDefaults()
	require.NoError(t, err)
	require.Equal(t, defaultLogDir(), cfg.LogDir)
	require.Contains(t, cfg.LogDir, strconv.Itoa(os.Getuid()))
	require.NotEqual(t, filepath.Join(os.TempDir(), "tau-build-logs"), cfg.LogDir)
}

func TestASlowAssetStoreDoesNotStallEveryOtherJobOnTheNode(t *testing.T) {
	const elsewhere = "github.com/acme/unrelated"

	h := newHarness(t)
	h.repos.add(elsewhere)
	id := h.push(t, testRef, testCommit)

	storing := make(chan struct{})
	release := make(chan struct{})
	h.assets.block = release
	h.assets.onPut = func() { close(storing) }

	h.runner.run = func(_ context.Context, _ Work, out Output) error {
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
	id := h.push(t, testRef, testCommit)

	// ONCE: the flap is this run's, and the rebuild below has to be allowed to
	// finish so that the second log it writes can be counted.
	h.assets.onStored = sync.OnceFunc(func() { h.store.stepDown() })
	// The attempt is in what the build prints so that the two runs are two
	// distinct objects: the store is content-addressed, so a rebuild printing
	// the same bytes would be the same cid and there would be nothing to count.
	h.runner.run = func(_ context.Context, w Work, out Output) error {
		fmt.Fprintf(out, "attempt %d printed this\n", w.Attempt)
		return nil
	}
	orphaned := assetCID([]byte("attempt 1 printed this\n"))

	_, err := h.step(t.Context())
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))

	h.store.stepUp()

	require.Equal(t, 1, h.assets.objects(), "the store kept the log")
	require.Equal(t, "attempt 1 printed this\n", h.assets.content(t, orphaned))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusClaimed, rec.status())
	require.Empty(t, rec.Logs, "and no job names it")

	ready, claimed := h.work.Depth()
	require.Zero(t, ready)
	require.Zero(t, claimed, "the ack landed, so there is no delivery left either")

	// AND WHAT REPAIRS THE JOB DOES NOT REPAIR THE OBJECT. reconcile pushes the
	// record back once it is past its cutoff, and the rebuild writes its own
	// log — a second object, beside the one nothing names.
	h.clock.advance(h.cfg.Queue.VisibilityTimeout + h.cfg.BuildTimeout + time.Minute)
	require.NoError(t, h.reconcile())
	require.True(t, h.mustStep(t))
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, h.status(t, id))

	after := h.mustRecord(t, id)
	require.Equal(t, map[string]string{stamp(2): assetCID([]byte("attempt 2 printed this\n"))},
		after.Logs, "the job names the rebuild's log and only that one")
	require.Equal(t, 2, h.assets.objects(),
		"and the first is still there, named by nothing, with nothing in this service able to "+
			"unreference it")
	require.Equal(t, "attempt 1 printed this\n", h.assets.content(t, orphaned))
}

func TestAJobReopenedWhileItsLogWasStoredKeepsThatLog(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	storing := make(chan struct{})
	release := make(chan struct{})
	h.assets.block = release
	h.assets.onPut = func() { close(storing) }

	h.runner.run = func(_ context.Context, _ Work, out Output) error {
		fmt.Fprint(out, "what the cancelled attempt printed\n")
		return nil
	}

	stepped := make(chan error, 1)
	go func() { _, err := h.step(context.Background()); stepped <- err }()
	<-storing

	// Both transitions take jobLock, which the publish is no longer holding.
	require.NoError(t, h.cancel(id))
	require.NoError(t, h.retry(id))
	close(release)
	require.NoError(t, <-stepped)

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusOpen, rec.status(),
		"the run that was cancelled does not get to write over the attempt that replaced it")
	require.Equal(t,
		map[string]string{stamp(1): assetCID([]byte("what the cancelled attempt printed\n"))},
		rec.Logs, "and what it printed is still readable, under its own attempt")
	require.Equal(t, 1, h.assets.objects())

	ready, _ := h.work.Depth()
	require.Equal(t, 1, ready, "the retry's item is there, and the requeue deduplicated against it")
}

func TestARecordThatWentWhileItsLogWasStoredIsNotWrittenBack(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	storing := make(chan struct{})
	release := make(chan struct{})
	h.assets.block = release
	h.assets.onPut = func() { close(storing) }
	h.runner.run = func(_ context.Context, _ Work, out Output) error {
		fmt.Fprint(out, "built\n")
		return nil
	}

	stepped := make(chan error, 1)
	go func() { _, err := h.step(context.Background()); stepped <- err }()
	<-storing

	require.NoError(t, h.store.MockCluster.Delete(jobRecordKey(id), time.Second))
	close(release)
	require.NoError(t, <-stepped)

	_, found, err := h.readRecord(id)
	require.NoError(t, err)
	require.False(t, found, "a record nobody put back is a record that stayed gone")
	require.Equal(t, 1, h.assets.objects(), "and the log the store already took stays with it")
}

func TestAReopenedJobsLogReportsAStoreThatRefusedIt(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	storing := make(chan struct{})
	release := make(chan struct{})
	h.assets.block = release
	h.assets.onPut = func() { close(storing) }
	h.runner.run = func(_ context.Context, _ Work, out Output) error {
		fmt.Fprint(out, "built\n")
		return nil
	}

	stepped := make(chan error, 1)
	go func() { _, err := h.step(context.Background()); stepped <- err }()
	<-storing

	require.NoError(t, h.cancel(id))
	require.NoError(t, h.retry(id))
	h.store.failBatches(errors.New("the log is written and the store said no"))
	close(release)

	err := <-stepped
	require.ErrorContains(t, err, "the store said no")
	require.ErrorContains(t, err, "recording the output of")
}
