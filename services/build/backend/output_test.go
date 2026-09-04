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

	buildv1 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v1"
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
	h := newHarness(t, withoutAssets())
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
	require.Equal(t, buildv1.JobStatus_JobStatusSuccess, rec.status())
	require.Empty(t, rec.Logs)
}

// THE CAP IS ADMISSION CONTROL ON A STRANGER'S OUTPUT — the build script that
// decides how much to print is in a repository this cloud was asked to build.
// Past it the log says so and the build carries on: a verbose build is not a
// failed one, and the runner is not told otherwise.
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
	require.Equal(t, buildv1.JobStatus_JobStatusSuccess, rec.status())
	stored := h.assets.content(t, rec.Logs[stamp(1)])
	require.Contains(t, stored, "0123456789this ")
	require.Contains(t, stored, "truncated", "a truncated log that does not say so is a log that lies")
}

// A RUN THIS NODE CANNOT OPEN A LOG FOR IS A FAILED BUILD. Running an hour of
// somebody's CI and then having nothing to show for it is worse than handing
// the job to a node whose disk works.
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

	p, err := out.publish(t.Context())
	require.NoError(t, err)
	require.Equal(t, "one line\n", h.assets.content(t, p.logCID))

	out.discard()
	out.discard()
	require.Equal(t, 1, h.assets.count(), "a second ending must not store the log again")
	require.Empty(t, logFiles(t, h))
}

// A LOG WHOSE NAME IS GONE IS STILL THE LOG. This is what the log directory
// being somewhere the OS sweeps costs, which is nothing: the run opened the
// file once, and what it holds is the inode. A publish that had to open the
// path again found nothing, stored nothing, and said nothing about it.
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

// AND A LOG THIS NODE REALLY CANNOT READ IS A FAILURE IT REPORTS, never an
// empty map handed back as if there were nothing to say.
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

// A LOG THAT LOST BYTES IS NOT PUBLISHED AS A COMPLETE ONE, and this is the
// production end of livelog's read order: a failure a reader hits is asked
// about BEFORE the close, because every read of a whole log is a read of a
// closed one. Here the file gets shorter than the log says it is — somebody
// outside this process rewrote it — which is the fault a reader stops on
// instead of reporting whatever is at that offset now.
//
// WHAT MUST NOT HAPPEN IS THE SETTLE THIS DEFECT CAME FROM: a job carrying
// Success and Logs: map[], with nothing anywhere saying that a build's output
// was lost. A build whose log this node cannot read back is a failed build, and
// it says which failure it was.
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
	require.Equal(t, buildv1.JobStatus_JobStatusFailed, rec.status(),
		"a log this node cannot read back is a failed build, not a build with no log")
	require.Contains(t, rec.Reason, "reading back the log it wrote")
	require.Empty(t, rec.Logs, "and nothing names a short log")
	require.Zero(t, h.assets.objects(), "which the store did not take either")
}

// AND THE READER IS ASKED EVEN WHEN THE STORE SAID YES.
//
// Put returns ONE error for two faults — its own and the reader's — so a store
// that swallowed a read error and answered with a cid has stored something that
// is not this log, and the record naming it would be worse than a record with
// no log at all: it reads as a complete build log for as long as anybody looks
// at it.
func TestAStoreThatSwallowedAReadErrorDoesNotGetToNameTheLog(t *testing.T) {
	h := newHarness(t, WithAssets(&swallowingStore{}))
	id := h.push(t, testRef, testCommit)

	h.runner.run = func(_ context.Context, w Work, out Output) error {
		fmt.Fprint(out, "cloning\n")
		return os.Truncate(logFile(h, w), 0)
	}
	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv1.JobStatus_JobStatusFailed, rec.status())
	require.Contains(t, rec.Reason, "reading back the log it wrote")
	require.Empty(t, rec.Logs, "the cid it answered with names bytes that are not the log")
}

// AND A STORE THAT STOPPED READING DOES NOT GET TO NAME IT EITHER.
//
// A read that ENDED EARLY raises no error anywhere: the reader was never asked
// for the rest, so it has nothing to report, and Put answers with the cid of
// whatever it took. What that cid addresses is a PREFIX of the build's output —
// and a job pointing at it says "here is the log", which is the same lie as
// Success with an empty map, only harder to notice. So the publish counts what
// left the file and compares it with what the log says it holds.
func TestAStoreThatStoppedReadingDoesNotGetToNameTheLog(t *testing.T) {
	h := newHarness(t, WithAssets(&shortStore{}))
	id := h.push(t, testRef, testCommit)

	h.runner.run = func(_ context.Context, _ Work, out Output) error {
		fmt.Fprint(out, "cloning\ncompiling\nlinking\n")
		return nil
	}
	require.True(t, h.mustStep(t))

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv1.JobStatus_JobStatusFailed, rec.status())
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

	// AND IT SHIPS NOTHING EITHER, which is why the publish is asked for only
	// after a section has found a record for the cid to reach. The store keeps
	// what it is handed and nothing in this service unreferences an asset, so a
	// log sent on this path is a permanent object no job will ever name.
	require.Zero(t, h.assets.count(),
		"there is no record to name a log, so the log does not go")
}

// A RUN THAT PRODUCED NOTHING WRITES NOTHING ONTO A TERMINAL RECORD. A
// replicated entry that changes no field is a cost with no fact in it, and this
// is the path every cancelled job takes on a node with no asset store.
func TestACancelledJobWithNoOutputCostsNoEntry(t *testing.T) {
	h := newHarness(t, withoutAssets())
	id := h.push(t, testRef, testCommit)

	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.finishNow(t, id, buildv1.JobStatus_JobStatusCancelled, "cancelled"))

	out, err := h.output(Work{JobID: id, Attempt: 1})
	require.NoError(t, err)
	defer out.discard()
	fmt.Fprint(out, "nowhere to put this\n")

	before := h.store.appliedEntries()
	require.NoError(t, h.settle(t.Context(), d, out, nil))
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

	out, err := h.output(Work{JobID: id, Attempt: 3})
	require.NoError(t, err)
	defer out.discard()
	fmt.Fprint(out, "what run three printed\n")

	require.NoError(t, h.settle(t.Context(), d, out, nil))

	require.Equal(t, map[string]string{stamp(3): assetCID([]byte("what run three printed\n"))},
		h.mustRecord(t, id).Logs)
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

// A LOG PATH IS PUBLIC, SO THE FILE AT IT MUST BE ONE THIS NODE MADE. Both
// halves of the name are derived — seg(jobID) is a hash of the repository, the
// ref and the commit, and jobid.go says out loud that being deterministic makes
// it guessable — and the open truncates. So a symbolic link planted at that
// path before the build starts is a request to truncate whatever it points at
// with this node's privileges, and the run then unlinks the link, which takes
// the evidence with it.
//
// MEASURED ON THE TREE BEFORE THE FIX: the victim below came back holding the
// build's output and the job read Success. The only precondition was somebody
// creating the log directory first, which os.MkdirAll accepted whoever owned it
// and whatever its mode.
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
	require.Equal(t, buildv1.JobStatus_JobStatusFailed, rec.status(),
		"a node that cannot open a log of its own is not the node that runs the build")
	require.Contains(t, rec.Reason, id)
}

// THE OTHER HALF IS THE DIRECTORY, and it is the half that decides whether the
// name above can be created at all. A directory this service owns at 0700 is
// one nobody else can plant anything in, and it is what makes every path-shaped
// step in a run safe for the ordinary reason.
func TestTheLogDirectoryTheServiceUsesIsOneNobodyElseCanWriteIn(t *testing.T) {
	shared := filepath.Join(t.TempDir(), "tau-build-logs")
	require.NoError(t, os.Mkdir(shared, 0o777))

	h := newHarnessCfg(t, func(c *Config) {
		c.LogDir = shared
	})
	id := h.push(t, testRef, testCommit)
	require.True(t, h.mustStep(t))
	require.Equal(t, buildv1.JobStatus_JobStatusSuccess, h.status(t, id))

	fi, err := os.Lstat(shared)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), fi.Mode().Perm(),
		"a directory an operator pointed at something shared is tightened before a run writes in it")
}

// AND THE DEFAULT IS NOT A NAME EVERY ACCOUNT ON THE BOX COMPETES FOR. It was
// one flat "tau-build-logs" under the OS temp dir, which on linux is a single
// /tmp: whoever creates it owns it, and every other account's node is then a
// node that cannot own its own log directory.
func TestTheDefaultLogDirectoryIsThisAccountsAlone(t *testing.T) {
	cfg, err := Config{}.withDefaults()
	require.NoError(t, err)
	require.Equal(t, defaultLogDir(), cfg.LogDir)
	require.Contains(t, cfg.LogDir, strconv.Itoa(os.Getuid()))
	require.NotEqual(t, filepath.Join(os.TempDir(), "tau-build-logs"), cfg.LogDir)
}

// A SECTION IS jobLock, AND jobLock IS ONE LOCK OVER THE WHOLE JOB TABLE. Every
// transition of every job on this node takes it — a claim, a settle, a Cancel,
// a Retry, an Enqueue's resume, the dead-letter drain, every row of a reconcile
// pass — so a round trip to the asset store made while it is held is one slow
// storage node stalling WEBHOOK INGESTION and every user-facing cancel and
// retry, for up to PublishTimeout per settled build. section.go, output.go and
// population_test.go all say a Put must not be inside one; this is the
// measurement that says the code agrees.
//
// MEASURED BEFORE THE PUBLISH MOVED OUT: the enqueue below was still blocked
// after 300ms with the store stalled, and completed 0.58ms after it was
// released.
//
// THE PROBE IS AN UNRELATED REPOSITORY on purpose. Two transitions of two
// different jobs already serialize against each other by design (section.go
// argues why one table-wide mutex is the right price); what must not happen is
// that they serialize behind a NETWORK CALL.
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
		_, err := h.enqueue(context.Background(), &buildv1.PushEvent{
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
	require.Equal(t, buildv1.JobStatus_JobStatusSuccess, h.status(t, id))
}

// THE ONE THING A LOG'S TRIP TO THE STORE IS NOT IS ATOMIC WITH THE RECORD,
// and this is that measured rather than argued. output.go says a cid reaches a
// job only for a log that reached the store; the CONVERSE — that a log reaches
// the store only if a cid for it reaches a job — was written down as a
// biconditional for one round and is false, and a claim that is false is worse
// than the weaker one that holds.
//
// THE STORE HERE TRIPS A LEADERSHIP FLAP THE INSTANT IT HAS TAKEN THE BYTES,
// which is the one interleaving that produces it: everything before the object
// is kept is refused by the ack, and everything after it is a record write this
// node can no longer make. What is left is an object in the asset store that no
// record names — and nothing in this service unreferences an asset (config.go)
// — plus a job that reads Claimed with no queue item, which reconcile pushes
// back once it is older than its cutoff so that the rebuild writes a SECOND log.
//
// doc.go names it under what is not covered at all, with its width and its
// precondition. This test is what makes that entry a fact: a round that closes
// the window has to delete this.
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
	require.Equal(t, buildv1.JobStatus_JobStatusClaimed, rec.status())
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
	require.Equal(t, buildv1.JobStatus_JobStatusSuccess, h.status(t, id))

	after := h.mustRecord(t, id)
	require.Equal(t, map[string]string{stamp(2): assetCID([]byte("attempt 2 printed this\n"))},
		after.Logs, "the job names the rebuild's log and only that one")
	require.Equal(t, 2, h.assets.objects(),
		"and the first is still there, named by nothing, with nothing in this service able to "+
			"unreference it")
	require.Equal(t, "attempt 1 printed this\n", h.assets.content(t, orphaned))
}

// A CANCEL AND A RETRY CAN LAND WHILE THE LOG IS GOING TO THE STORE, and that
// is the window the second section exists to read again. The first section
// found a record this run's outcome was about, so the bytes went; by the time
// the cid comes back the record is a NEW attempt that this run knows nothing
// about, and this run's outcome must not be written over it.
//
// WHAT MUST NOT HAPPEN IS THE LOG BEING DROPPED. The store has kept it and
// nothing in this service unreferences an asset, so a branch that returned
// without recording the cid would leave a permanent object no job names — the
// orphan the ack in front of the publish exists to prevent, made by the branch
// after it instead. It is filed under the RUN'S attempt, beside whatever the
// rebuild writes.
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
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, rec.status(),
		"the run that was cancelled does not get to write over the attempt that replaced it")
	require.Equal(t,
		map[string]string{stamp(1): assetCID([]byte("what the cancelled attempt printed\n"))},
		rec.Logs, "and what it printed is still readable, under its own attempt")
	require.Equal(t, 1, h.assets.objects())

	ready, _ := h.work.Depth()
	require.Equal(t, 1, ready, "the retry's item is there, and the requeue deduplicated against it")
}

// THE RECORD CAN GO WHILE THE LOG IS GOING TO THE STORE, and the second section
// reads again for that as much as for the three states settle lists. There is
// nothing left to name the log with, so nothing is written — and the object the
// store already took is one nothing will ever name, which is the same window
// doc.go records under what is not covered at all, reached by a different
// precondition.
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

// AND THE REOPENED JOB'S LOG IS A WRITE LIKE ANY OTHER: a store that refuses it
// is reported, rather than swallowed on the way to the push.
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
