package backend

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/services/build/livelog"
)

// collector is a ServerStream's Send, which is all the handler uses.
type collector struct {
	mu     sync.Mutex
	chunks []*buildv2.LogChunk
	fail   error
}

func (c *collector) send(chunk *buildv2.LogChunk) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail != nil {
		return c.fail
	}
	c.chunks = append(c.chunks, &buildv2.LogChunk{
		Data:       append([]byte(nil), chunk.GetData()...),
		NextOffset: chunk.GetNextOffset(),
	})
	return nil
}

func (c *collector) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []byte
	for _, chunk := range c.chunks {
		out = append(out, chunk.GetData()...)
	}
	return string(out)
}

func (c *collector) last() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.chunks) == 0 {
		return 0
	}
	return c.chunks[len(c.chunks)-1].GetNextOffset()
}

// running puts a log on this service under jobID, as that attempt's run would.
func running(t *testing.T, s *Service, jobID string, attempt uint32) *livelog.Log {
	t.Helper()

	log, err := livelog.Create(t.TempDir() + "/log")
	require.NoError(t, err)

	s.logsMu.Lock()
	s.logs[jobID] = runLog{attempt: attempt, log: log}
	s.logsMu.Unlock()

	t.Cleanup(func() {
		s.logsMu.Lock()
		delete(s.logs, jobID)
		s.logsMu.Unlock()
		log.Close()
	})
	return log
}

func TestFollowStreamsARunningBuild(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	log := running(t, h.Service, id, 1)

	_, err := log.Write([]byte("cloning\n"))
	require.NoError(t, err)

	var out collector
	done := make(chan error, 1)
	go func() {
		done <- h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1}, out.send)
	}()

	// What was already there arrives without waiting for more.
	require.Eventually(t, func() bool { return out.text() == "cloning\n" }, 2*time.Second, 5*time.Millisecond)

	// And so does what comes next, while the follower is still attached.
	_, err = log.Write([]byte("compiling\n"))
	require.NoError(t, err)
	require.Eventually(t, func() bool { return out.text() == "cloning\ncompiling\n" }, 2*time.Second, 5*time.Millisecond)

	// The build ending is a complete answer, not an error: livelog blocks rather than
	// returning EOF early.
	require.NoError(t, log.Close())
	require.NoError(t, <-done)

	require.Equal(t, int64(len("cloning\ncompiling\n")), out.last(),
		"the offset a resuming client sends back is carried per chunk")
}

func TestFollowResumesAtAnOffset(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	log := running(t, h.Service, id, 1)

	_, err := log.Write([]byte("first\nsecond\n"))
	require.NoError(t, err)
	require.NoError(t, log.Close())

	var out collector
	require.NoError(t, h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1, Offset: int64(len("first\n"))}, out.send))
	require.Equal(t, "second\n", out.text())
}

func TestFollowOnANodeThatIsNotTheWriter(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	err := h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1}, (&collector{}).send)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))

	// The narrowing is real: the other refusals keep their own codes, so a
	// caller does not walk the whole fleet over a bad request.
	err = h.follow(t.Context(), &buildv2.FollowRequest{Attempt: 1}, (&collector{}).send)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	err = h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1, Offset: -1}, (&collector{}).send)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	// An offset past the end of a log that IS here is the caller's arithmetic,
	// not a routing problem.
	running(t, h.Service, id, 1)
	err = h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1, Offset: 4096}, (&collector{}).send)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestFollowEndsWhenTheViewerGoesAway(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	log := running(t, h.Service, id, 1)

	_, err := log.Write([]byte("started\n"))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	var out collector
	done := make(chan error, 1)
	go func() { done <- h.follow(ctx, &buildv2.FollowRequest{JobId: id, Attempt: 1}, out.send) }()

	require.Eventually(t, func() bool { return out.text() == "started\n" }, 2*time.Second, 5*time.Millisecond)

	cancel()
	// The build never ends; the follow does.
	_, err = log.Write([]byte("still going\n"))
	require.NoError(t, err)

	select {
	case err := <-done:
		require.NoError(t, err, "a viewer hanging up is not a failure of the build")
	case <-time.After(5 * time.Second):
		t.Fatal("the follow outlived the viewer")
	}
}

func TestFollowStopsWhenItCannotSend(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	log := running(t, h.Service, id, 1)

	_, err := log.Write([]byte("output\n"))
	require.NoError(t, err)

	broken := &collector{fail: errors.New("client went away")}
	require.ErrorContains(t, h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1}, broken.send), "client went away")

}

// heldBuild is a build in the middle of its work: it has printed its first
// line and prints each line it is handed until it is finished.
type heldBuild struct {
	lines chan string
	once  sync.Once
	ended chan error
}

func (b *heldBuild) say(line string) { b.lines <- line }

func (b *heldBuild) finish() { b.once.Do(func() { close(b.lines) }) }

// hold starts the pushed job's build and returns once it has printed first.
func hold(t *testing.T, h *harness, first string) *heldBuild {
	t.Helper()
	b := &heldBuild{lines: make(chan string), ended: make(chan error, 1)}
	started := make(chan struct{})
	h.runner.run = func(_ context.Context, _ Work, out Output) error {
		fmt.Fprint(out, first)
		close(started)
		for line := range b.lines {
			fmt.Fprint(out, line)
		}
		return nil
	}
	go func() {
		_, err := h.step(context.WithoutCancel(t.Context()))
		b.ended <- err
	}()
	t.Cleanup(b.finish)
	<-started
	return b
}

// snapshotOf is what id's log holds from `from`. It must come back while the
// build runs: a read that waited for the build would not.
func snapshotOf(t *testing.T, h *harness, id string, from int64) *collector {
	t.Helper()
	var out collector
	done := make(chan error, 1)
	go func() {
		done <- h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1, Offset: from, Snapshot: true}, out.send)
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("a snapshot of a running build waited for the build to end")
	}
	return &out
}

func TestASnapshotEndsAtWhatTheBuildHasWrittenAndAFollowEndsWithTheBuild(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	build := hold(t, h, "cloning\n")

	soFar := snapshotOf(t, h, id, 0)
	require.Equal(t, "cloning\n", soFar.text())
	require.Equal(t, int64(len("cloning\n")), soFar.last())

	var followed collector
	following := make(chan error, 1)
	go func() {
		following <- h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1}, followed.send)
	}()
	require.Eventually(t, func() bool { return followed.text() == "cloning\n" }, 2*time.Second, 5*time.Millisecond)

	build.say("compiling\n")
	require.Eventually(t, func() bool { return followed.text() == "cloning\ncompiling\n" }, 2*time.Second, 5*time.Millisecond)
	select {
	case err := <-following:
		t.Fatalf("the follow ended (%v) while the build was still running", err)
	default:
	}

	require.Equal(t, "cloning\ncompiling\n", snapshotOf(t, h, id, 0).text())
	require.Equal(t, "compiling\n", snapshotOf(t, h, id, int64(len("cloning\n"))).text())
	require.Empty(t, snapshotOf(t, h, id, int64(len("cloning\ncompiling\n"))).text(),
		"nothing has been written past that offset yet")

	build.finish()
	require.NoError(t, <-build.ended)
	select {
	case err := <-following:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the follow outlived the build")
	}

	// ENDED, THE LOG IS THE STORED ONE for either read: it is not on this node
	// any more, and the record names the whole of it.
	stored := h.assets.content(t, h.mustRecord(t, id).Logs[stamp(1)])
	require.Equal(t, "cloning\ncompiling\n", stored)
	require.Equal(t, stored, followed.text(), "the follower saw the whole log")
	for _, snapshot := range []bool{false, true} {
		err := h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1, Snapshot: snapshot}, (&collector{}).send)
		require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "snapshot=%v", snapshot)
	}
}

func TestALogNoNodeIsWritingIsComingUntilTheJobEnds(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	ask := func(jobID string, snapshot bool) error {
		return h.follow(t.Context(), &buildv2.FollowRequest{JobId: jobID, Attempt: 1, Snapshot: snapshot}, (&collector{}).send)
	}

	for _, snapshot := range []bool{false, true} {
		require.Equal(t, connect.CodeUnavailable, connect.CodeOf(ask(id, snapshot)), "queued, snapshot=%v", snapshot)
	}

	build := hold(t, h, "cloning\n")
	h.logsMu.Lock()
	delete(h.logs, id)
	h.logsMu.Unlock()
	require.Equal(t, buildv2.JobStatus_JobStatusClaimed, h.status(t, id))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(ask(id, false)), "claimed, written on another node")

	build.finish()
	require.NoError(t, <-build.ended)
	for _, snapshot := range []bool{false, true} {
		err := ask(id, snapshot)
		require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "ended, snapshot=%v", snapshot)
		require.ErrorContains(t, err, buildv2.JobStatus_JobStatusSuccess.String())
	}

	h.store.stepDown()
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(ask(id, false)), "a follower cannot read the record")
	h.store.stepUp()

	require.Equal(t, connect.CodeNotFound, connect.CodeOf(ask(strings.Repeat("ab", 32), false)), "no such job")
}

func TestAnEndedLogIsWholeToAFollowAndASnapshot(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	log := running(t, h.Service, id, 1)

	_, err := log.Write([]byte("cloning\ncompiling\n"))
	require.NoError(t, err)
	require.NoError(t, log.Close())

	for _, snapshot := range []bool{false, true} {
		var out collector
		require.NoError(t, h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1, Snapshot: snapshot}, out.send))
		require.Equal(t, "cloning\ncompiling\n", out.text(), "snapshot=%v", snapshot)
		require.Equal(t, int64(len("cloning\ncompiling\n")), out.last(), "snapshot=%v", snapshot)
	}
}

func TestASnapshotIsRefusedWhereAFollowIs(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	ask := func(jobID string, offset int64) error {
		return h.follow(t.Context(), &buildv2.FollowRequest{JobId: jobID, Attempt: 1, Offset: offset, Snapshot: true}, (&collector{}).send)
	}

	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(ask(id, 0)))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(ask("", 0)))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(ask(id, -1)))

	running(t, h.Service, id, 1)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(ask(id, 4096)))
}

// ask is one read of jobID's log at that run and offset, and what it got.
func ask(t *testing.T, h *harness, jobID string, attempt int32, from int64, snapshot bool) (string, error) {
	t.Helper()
	var out collector
	err := h.follow(t.Context(), &buildv2.FollowRequest{JobId: jobID, Attempt: attempt, Offset: from, Snapshot: snapshot}, out.send)
	return out.text(), err
}

func TestAResumeIsNeverAnotherRunsLog(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	first := "first run, line one\nfirst run, line two\n"
	h.runner.run = func(_ context.Context, _ Work, out Output) error {
		fmt.Fprint(out, first)
		return errors.New("the node went away")
	}
	require.True(t, h.mustStep(t))
	require.NoError(t, h.retry(id))
	hold(t, h, "second run, line one\nsecond run, line two\n")
	require.Equal(t, uint32(2), h.mustRecord(t, id).Attempt)

	read := int64(len("first run, line one\n"))
	for _, snapshot := range []bool{false, true} {
		// A follow handed anything stops there, so a splice fails this rather
		// than waiting on the held build.
		ctx, stop := context.WithCancel(t.Context())
		var out collector
		err := h.follow(ctx, &buildv2.FollowRequest{JobId: id, Attempt: 1, Offset: read, Snapshot: snapshot}, func(c *buildv2.LogChunk) error {
			stop()
			return out.send(c)
		})
		stop()
		require.Empty(t, out.text(), "snapshot=%v: the second run's bytes are not the rest of the first run's log", snapshot)
		require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "snapshot=%v", snapshot)
	}

	got, err := ask(t, h, id, 2, read, true)
	require.NoError(t, err)
	require.Equal(t, "second run, line one\nsecond run, line two\n"[read:], got)
}

func TestWhetherARunHasEndedIsTheRecords(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	code := func(attempt int32) connect.Code {
		_, err := ask(t, h, id, attempt, 0, false)
		return connect.CodeOf(err)
	}

	require.Equal(t, connect.CodeInvalidArgument, code(0), "an offset needs its run")
	require.Equal(t, connect.CodeUnavailable, code(1), "queued: its first run is to come")
	require.Equal(t, connect.CodeInvalidArgument, code(2))

	build := hold(t, h, "cloning\n")
	h.logsMu.Lock()
	delete(h.logs, id)
	h.logsMu.Unlock()
	require.Equal(t, connect.CodeUnavailable, code(1), "claimed, written on another node")
	require.Equal(t, connect.CodeUnavailable, code(2), "the run after the claimed one is to come")
	build.finish()
	require.NoError(t, <-build.ended)

	require.NoError(t, h.retry(id))
	require.Equal(t, connect.CodeFailedPrecondition, code(1), "retried: the job has moved past it")
	require.Equal(t, connect.CodeUnavailable, code(2), "retried: the next run is to come")
	require.Equal(t, connect.CodeInvalidArgument, code(3))

	require.NoError(t, h.cancel(id))
	require.Equal(t, connect.CodeFailedPrecondition, code(1))
	require.Equal(t, connect.CodeFailedPrecondition, code(2), "the job ended before it")
	require.Equal(t, connect.CodeInvalidArgument, code(3))
}

// storing holds the asset store's next Put until release is called, and closes
// reached once a publish is inside it.
func storing(t *testing.T, h *harness) (reached chan struct{}, release func()) {
	reached, held := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(held) }) }
	t.Cleanup(release)
	h.assets.mu.Lock()
	h.assets.block = held
	h.assets.onPut = func() { close(reached) }
	h.assets.mu.Unlock()
	return reached, release
}

func TestALogIsWholeWhileItIsHandedToTheStore(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	h.runner.run = func(_ context.Context, _ Work, out Output) error {
		fmt.Fprint(out, "cloning\ncompiling\n")
		return nil
	}
	reached, release := storing(t, h)
	done := make(chan error, 1)
	go func() {
		_, err := h.step(context.WithoutCancel(t.Context()))
		done <- err
	}()
	<-reached
	require.Equal(t, buildv2.JobStatus_JobStatusClaimed, h.status(t, id))

	for _, snapshot := range []bool{true, false} {
		got, err := ask(t, h, id, 1, 0, snapshot)
		require.NoError(t, err, "snapshot=%v", snapshot)
		require.Equal(t, "cloning\ncompiling\n", got, "snapshot=%v", snapshot)
	}

	release()
	require.NoError(t, <-done)
	require.Equal(t, "cloning\ncompiling\n", h.assets.content(t, h.mustRecord(t, id).Logs[stamp(1)]))
	_, err := ask(t, h, id, 1, 0, true)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "stored and named: the node has let go")
}

func TestACancelledRunsLogIsFollowedUntilItIsStored(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	h.runner.run = func(_ context.Context, _ Work, out Output) error {
		fmt.Fprint(out, "the first run\n")
		return errors.New("flaky")
	}
	require.True(t, h.mustStep(t))
	require.NoError(t, h.retry(id))

	started := make(chan struct{})
	h.runner.run = func(ctx context.Context, _ Work, out Output) error {
		fmt.Fprint(out, "the second run\n")
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	reached, release := storing(t, h)
	done := make(chan error, 1)
	go func() {
		_, err := h.step(context.WithoutCancel(t.Context()))
		done <- err
	}()
	<-started
	require.NoError(t, h.cancel(id))
	<-reached

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusCancelled, rec.status())
	require.Equal(t, []string{stamp(1)}, slices.Sorted(maps.Keys(rec.Logs)), "the record does not name this run's log yet")

	var during []string
	for _, snapshot := range []bool{true, false} {
		got, err := ask(t, h, id, 2, 0, snapshot)
		require.NoError(t, err, "snapshot=%v", snapshot)
		require.True(t, strings.HasPrefix(got, "the second run\n"), "snapshot=%v: %q", snapshot, got)
		during = append(during, got)
	}

	release()
	require.NoError(t, <-done)
	stored := h.assets.content(t, h.mustRecord(t, id).Logs[stamp(2)])
	require.Equal(t, []string{stored, stored}, during, "what was followed is what was stored")
	_, err := ask(t, h, id, 2, 0, false)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
}
