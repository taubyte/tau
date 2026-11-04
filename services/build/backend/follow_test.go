package backend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/services/build/joblog"
	"github.com/taubyte/tau/services/build/livelog"
)

// collector is a ServerStream's Send, which is all the handler uses. Each event it
// takes rings, so a test waits for what it expects rather than for a while.
type collector struct {
	mu      sync.Mutex
	events  []*buildv2.Job_Event
	fail    error
	changed chan struct{}
}

func (c *collector) send(ev *buildv2.Job_Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail != nil {
		return c.fail
	}
	c.events = append(c.events, proto.Clone(ev).(*buildv2.Job_Event))
	if c.changed != nil {
		close(c.changed)
		c.changed = nil
	}
	return nil
}

// waitFor blocks until what the collector holds satisfies ok. The test's own
// deadline is the only bound: a follow that never delivers is the failure.
func (c *collector) waitFor(ok func(*collector) bool) {
	for {
		c.mu.Lock()
		if ok(c.locked()) {
			c.mu.Unlock()
			return
		}
		if c.changed == nil {
			c.changed = make(chan struct{})
		}
		changed := c.changed
		c.mu.Unlock()
		<-changed
	}
}

// locked is the collector as ok reads it, under its lock.
func (c *collector) locked() *collector { return &collector{events: c.events} }

func (c *collector) waitText(want string) {
	c.waitFor(func(c *collector) bool { return c.textLocked() == want })
}

func (c *collector) waitStatus(want buildv2.JobStatus) {
	c.waitFor(func(c *collector) bool { return slices.Contains(c.statusesLocked(), want) })
}

// text is the messages, each a line.
func (c *collector) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.textLocked()
}

func (c *collector) textLocked() string { return messagesOf(c.events) }

func (c *collector) statuses() []buildv2.JobStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusesLocked()
}

func (c *collector) statusesLocked() []buildv2.JobStatus { return statusesOf(c.events) }

func (c *collector) last() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.events) == 0 {
		return 0
	}
	return c.events[len(c.events)-1].GetOffset()
}

// after is the offset after the nth event.
func (c *collector) after(n int) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.events[n].GetOffset()
}

func messagesOf(evs []*buildv2.Job_Event) string {
	var out strings.Builder
	for _, ev := range evs {
		if m, ok := ev.GetKind().(*buildv2.Job_Event_Message); ok {
			out.WriteString(m.Message + "\n")
		}
	}
	return out.String()
}

func statusesOf(evs []*buildv2.Job_Event) []buildv2.JobStatus {
	var out []buildv2.JobStatus
	for _, ev := range evs {
		if st, ok := ev.GetKind().(*buildv2.Job_Event_Status); ok {
			out = append(out, st.Status)
		}
	}
	return out
}

// testRun is a log on this service under a job, as that attempt's run would write
// it.
type testRun struct {
	t   testing.TB
	log *livelog.Log
}

func (r *testRun) say(line string) {
	r.t.Helper()
	require.NoError(r.t, joblog.Write(r.log, &buildv2.Job_Event{Kind: &buildv2.Job_Event_Message{Message: line}}))
}

func (r *testRun) status(st buildv2.JobStatus) {
	r.t.Helper()
	require.NoError(r.t, joblog.Write(r.log, &buildv2.Job_Event{Kind: &buildv2.Job_Event_Status{Status: st}}))
}

func (r *testRun) close() {
	r.t.Helper()
	require.NoError(r.t, r.log.Close())
}

// running puts a log on this service under jobID, as that attempt's run would.
func running(t *testing.T, s *Service, jobID string, attempt uint32) *testRun {
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
	return &testRun{t: t, log: log}
}

func TestFollowStreamsARunningBuild(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	run := running(t, h.Service, id, 1)
	run.status(buildv2.JobStatus_JobStatusClaimed)
	run.say("cloning")

	var out collector
	done := make(chan error, 1)
	go func() {
		done <- h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1}, out.send)
	}()

	// What was already there arrives without waiting for more.
	out.waitText("cloning\n")

	// And so does what comes next, while the follower is still attached.
	run.say("compiling")
	out.waitText("cloning\ncompiling\n")

	// The build ending is a complete answer, not an error: livelog blocks rather than
	// returning EOF early.
	run.status(buildv2.JobStatus_JobStatusSuccess)
	run.close()
	require.NoError(t, <-done)

	require.Equal(t, []buildv2.JobStatus{buildv2.JobStatus_JobStatusClaimed, buildv2.JobStatus_JobStatusSuccess}, out.statuses())
	require.Equal(t, run.log.Size(), out.last(), "the offset a resuming client sends back is carried per event")
}

func TestFollowResumesAtAnOffset(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	run := running(t, h.Service, id, 1)
	run.say("first")
	run.say("second")
	run.close()

	var whole collector
	require.NoError(t, h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1}, whole.send))

	var out collector
	require.NoError(t, h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1, Offset: whole.after(0)}, out.send))
	require.Equal(t, "second\n", out.text())
}

func TestFollowOnANodeThatIsNotTheWriter(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	// A NODE THAT DOES NOT LEAD holds no follow: the caller's pool walks on.
	h.store.stepDown()
	err := h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1}, (&collector{}).send)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	h.store.stepUp()

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
	id := h.planned(t, testRef, testCommit)
	run := running(t, h.Service, id, 1)
	run.say("started")

	ctx, cancel := context.WithCancel(t.Context())
	var out collector
	done := make(chan error, 1)
	go func() { done <- h.follow(ctx, &buildv2.FollowRequest{JobId: id, Attempt: 1}, out.send) }()

	out.waitText("started\n")

	cancel()
	// The build never ends; the follow does.
	run.say("still going")
	require.NoError(t, <-done, "a viewer hanging up is not a failure of the build")
}

func TestFollowStopsWhenItCannotSend(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	running(t, h.Service, id, 1).say("output")

	broken := &collector{fail: errors.New("client went away")}
	require.ErrorContains(t, h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1}, broken.send), "client went away")
}

func TestAFollowOfALogCutMidRecordIsUnavailable(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	run := running(t, h.Service, id, 1)
	var raw bytes.Buffer
	require.NoError(t, joblog.Write(&raw, &buildv2.Job_Event{Kind: &buildv2.Job_Event_Message{Message: "a line"}}))
	_, err := run.log.Write(raw.Bytes()[:raw.Len()-2])
	require.NoError(t, err)
	run.close()

	err = h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1}, (&collector{}).send)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
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
// build runs: a read that waited for the build would hang the test.
func snapshotOf(t *testing.T, h *harness, id string, from int64) *collector {
	t.Helper()
	var out collector
	require.NoError(t, h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1, Offset: from, Snapshot: true}, out.send))
	return &out
}

func TestASnapshotEndsAtWhatTheBuildHasWrittenAndAFollowEndsWithTheBuild(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	b := hold(t, h, "cloning\n")

	// A LINE IS A MESSAGE ONCE IT ENDS, so what is snapshotted is whole lines.
	soFar := snapshotOf(t, h, id, 0)
	require.Equal(t, "cloning\n", soFar.text())
	require.Equal(t, []buildv2.JobStatus{buildv2.JobStatus_JobStatusClaimed}, soFar.statuses(),
		"the log opens with the run's claim")

	var followed collector
	following := make(chan error, 1)
	go func() {
		following <- h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1}, followed.send)
	}()
	followed.waitText("cloning\n")

	b.say("compiling\n")
	followed.waitText("cloning\ncompiling\n")
	select {
	case err := <-following:
		t.Fatalf("the follow ended (%v) while the build was still running", err)
	default:
	}

	require.Equal(t, "cloning\ncompiling\n", snapshotOf(t, h, id, 0).text())
	require.Equal(t, "compiling\n", snapshotOf(t, h, id, soFar.last()).text())
	whole := snapshotOf(t, h, id, 0)
	require.Empty(t, snapshotOf(t, h, id, whole.last()).text(), "nothing has been written past that offset yet")

	b.finish()
	require.NoError(t, <-b.ended)
	require.NoError(t, <-following)

	// THE LOG ENDS WITH WHAT THE RUN LEFT THE JOB AS, so the follower knows how it
	// ended from the log itself.
	require.Equal(t, []buildv2.JobStatus{buildv2.JobStatus_JobStatusClaimed, buildv2.JobStatus_JobStatusSuccess}, followed.statuses())

	// ENDED, THE LOG IS THE STORED ONE for either read: it is not on this node
	// any more, and the record names the whole of it.
	stored := h.assets.events(t, h.mustRecord(t, id).Logs[stamp(1)])
	require.Equal(t, "cloning\ncompiling\n", messagesOf(stored))
	require.Equal(t, followed.statuses(), statusesOf(stored), "the stored log is everything that happened to the run")
	for _, snapshot := range []bool{false, true} {
		err := h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1, Snapshot: snapshot}, (&collector{}).send)
		require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "snapshot=%v", snapshot)
	}
}

// THE LEADER HOLDS A FOLLOW OF A RUN TO COME: every run is claimed there, so the
// follow is told the job is queued, and then the run as it happens.
func TestTheLeaderHoldsAFollowOfARunToCome(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	_, err := ask(t, h, id, 1, 0, true)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err), "a snapshot holds nothing")

	var out collector
	following := make(chan error, 1)
	go func() {
		following <- h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1}, out.send)
	}()
	out.waitStatus(buildv2.JobStatus_JobStatusOpen)

	b := hold(t, h, "cloning\n")
	out.waitText("cloning\n")
	b.finish()
	require.NoError(t, <-b.ended)
	require.NoError(t, <-following)
	require.Equal(t, []buildv2.JobStatus{
		buildv2.JobStatus_JobStatusOpen, buildv2.JobStatus_JobStatusClaimed, buildv2.JobStatus_JobStatusSuccess,
	}, out.statuses())
}

// AND A JOB THAT ENDS WITHOUT THE RUN ends the held follow: the commit that ends it
// rings it.
func TestAHeldFollowEndsWithAJobCancelledBeforeItRan(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	var out collector
	following := make(chan error, 1)
	go func() {
		following <- h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1}, out.send)
	}()
	out.waitStatus(buildv2.JobStatus_JobStatusOpen)

	require.NoError(t, h.cancel(id))
	err := <-following
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	require.ErrorContains(t, err, buildv2.JobStatus_JobStatusCancelled.String())
}

func TestALogNoNodeIsWritingIsElsewhereOrEnded(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	// CLAIMED BY ANOTHER NODE, the run is that node's to answer: the leader does not
	// hold it, and the caller's pool walks on.
	rec := h.mustRecord(t, id)
	rec.Status, rec.Attempt, rec.Holder = int32(buildv2.JobStatus_JobStatusClaimed), 1, "another-node"
	h.plantRecord(t, rec)
	for _, snapshot := range []bool{false, true} {
		_, err := ask(t, h, id, 1, 0, snapshot)
		require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err), "claimed elsewhere, snapshot=%v", snapshot)
	}

	// ENDED, it is the record's to say.
	h = newHarness(t)
	id = h.planned(t, testRef, testCommit)
	b := hold(t, h, "cloning\n")
	b.finish()
	require.NoError(t, <-b.ended)
	for _, snapshot := range []bool{false, true} {
		_, err := ask(t, h, id, 1, 0, snapshot)
		require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "ended, snapshot=%v", snapshot)
		require.ErrorContains(t, err, buildv2.JobStatus_JobStatusSuccess.String())
	}

	h.store.stepDown()
	_, err := ask(t, h, id, 1, 0, false)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err), "a follower cannot read the record")
	h.store.stepUp()

	_, err = ask(t, h, strings.Repeat("ab", 32), 1, 0, false)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "no such job")
}

func TestAnEndedLogIsWholeToAFollowAndASnapshot(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	run := running(t, h.Service, id, 1)
	run.say("cloning")
	run.say("compiling")
	run.close()

	for _, snapshot := range []bool{false, true} {
		var out collector
		require.NoError(t, h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1, Snapshot: snapshot}, out.send))
		require.Equal(t, "cloning\ncompiling\n", out.text(), "snapshot=%v", snapshot)
		require.Equal(t, run.log.Size(), out.last(), "snapshot=%v", snapshot)
	}
}

func TestASnapshotIsRefusedWhereAFollowIs(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	snap := func(jobID string, offset int64) error {
		_, err := ask(t, h, jobID, 1, offset, true)
		return err
	}

	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(snap(id, 0)))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(snap("", 0)))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(snap(id, -1)))

	running(t, h.Service, id, 1)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(snap(id, 4096)))
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
	id := h.planned(t, testRef, testCommit)

	h.runner.run = func(_ context.Context, _ Work, out Output) error {
		fmt.Fprint(out, "first run, line one\nfirst run, line two\n")
		return errors.New("the node went away")
	}
	require.True(t, h.mustStep(t))
	first := h.assets.events(t, h.mustRecord(t, id).Logs[stamp(1)])
	read := first[1].GetOffset()

	require.NoError(t, h.retry(id))
	hold(t, h, "second run, line one\nsecond run, line two\n")
	require.Equal(t, uint32(2), h.mustRecord(t, id).Attempt)

	for _, snapshot := range []bool{false, true} {
		// A follow handed anything stops there, so a splice fails this rather
		// than waiting on the held build.
		ctx, stop := context.WithCancel(t.Context())
		var out collector
		err := h.follow(ctx, &buildv2.FollowRequest{JobId: id, Attempt: 1, Offset: read, Snapshot: snapshot}, func(ev *buildv2.Job_Event) error {
			stop()
			return out.send(ev)
		})
		stop()
		require.Empty(t, out.text(), "snapshot=%v: the second run's events are not the rest of the first run's log", snapshot)
		require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "snapshot=%v", snapshot)
	}

	got, err := ask(t, h, id, 2, 0, true)
	require.NoError(t, err)
	require.Equal(t, "second run, line one\nsecond run, line two\n", got)
}

func TestWhetherARunHasEndedIsTheRecords(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	code := func(attempt int32) connect.Code {
		_, err := ask(t, h, id, attempt, 0, true)
		return connect.CodeOf(err)
	}

	require.Equal(t, connect.CodeInvalidArgument, code(0), "an offset needs its run")
	require.Equal(t, connect.CodeUnavailable, code(1), "queued: its first run is to come")
	require.Equal(t, connect.CodeInvalidArgument, code(2))

	b := hold(t, h, "cloning\n")
	h.logsMu.Lock()
	delete(h.logs, id)
	h.logsMu.Unlock()
	require.Equal(t, connect.CodeUnavailable, code(1), "claimed, written on another node")
	require.Equal(t, connect.CodeUnavailable, code(2), "the run after the claimed one is to come")
	b.finish()
	require.NoError(t, <-b.ended)

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
	id := h.planned(t, testRef, testCommit)
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
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, h.status(t, id), "the record settles before its log is stored")

	for _, snapshot := range []bool{true, false} {
		var out collector
		require.NoError(t, h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1, Snapshot: snapshot}, out.send), "snapshot=%v", snapshot)
		require.Equal(t, "cloning\ncompiling\n", out.text(), "snapshot=%v", snapshot)
		require.Equal(t, []buildv2.JobStatus{buildv2.JobStatus_JobStatusClaimed, buildv2.JobStatus_JobStatusSuccess}, out.statuses())
	}

	release()
	require.NoError(t, <-done)
	stored := h.assets.events(t, h.mustRecord(t, id).Logs[stamp(1)])
	require.Equal(t, "cloning\ncompiling\n", messagesOf(stored))
	_, err := ask(t, h, id, 1, 0, true)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "stored and named: the node has let go")
}

func TestACancelledRunsLogIsFollowedUntilItIsStored(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
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
	stored := h.assets.events(t, h.mustRecord(t, id).Logs[stamp(2)])
	require.Equal(t, []string{messagesOf(stored), messagesOf(stored)}, during, "what was followed is what was stored")
	require.Equal(t, buildv2.JobStatus_JobStatusCancelled, statusesOf(stored)[len(statusesOf(stored))-1],
		"and the log ends with the job's cancel")
	_, err := ask(t, h, id, 2, 0, false)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
}
