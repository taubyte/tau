package jobs

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
	"google.golang.org/protobuf/proto"

	builderv2 "github.com/taubyte/tau/pkg/proto/gen/tau/builder/v2"
	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
	"github.com/taubyte/tau/services/plm/run"
)

// collector is a ServerStream's Send, which is all the handler uses. Each event it
// takes rings, so a test waits for what it expects rather than for a while.
type collector struct {
	mu      sync.Mutex
	events  []*plmv2.Job_Event
	fail    error
	changed chan struct{}
}

func (c *collector) send(ev *plmv2.Job_Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail != nil {
		return c.fail
	}
	c.events = append(c.events, proto.Clone(ev).(*plmv2.Job_Event))
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

func (c *collector) waitStatus(want plmv2.JobStatus) {
	c.waitFor(func(c *collector) bool { return slices.Contains(c.statusesLocked(), want) })
}

// text is the messages, each a line.
func (c *collector) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.textLocked()
}

func (c *collector) textLocked() string { return messagesOf(c.events) }

func (c *collector) statuses() []plmv2.JobStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusesLocked()
}

func (c *collector) statusesLocked() []plmv2.JobStatus { return statusesOf(c.events) }

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

func messagesOf(evs []*plmv2.Job_Event) string {
	var out strings.Builder
	for _, ev := range evs {
		if m, ok := ev.GetKind().(*plmv2.Job_Event_Message); ok {
			out.WriteString(m.Message + "\n")
		}
	}
	return out.String()
}

func statusesOf(evs []*plmv2.Job_Event) []plmv2.JobStatus {
	var out []plmv2.JobStatus
	for _, ev := range evs {
		if st, ok := ev.GetKind().(*plmv2.Job_Event_Status); ok {
			out = append(out, st.Status)
		}
	}
	return out
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

// hold starts the pushed job's plm and returns once it has printed first.
func hold(t *testing.T, h *harness, first string) *heldBuild {
	t.Helper()
	b := &heldBuild{lines: make(chan string), ended: make(chan error, 1)}
	started := make(chan struct{})
	h.builder.run = func(_ context.Context, _ run.Work, out run.Output) error {
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
	require.NoError(t, h.follow(t.Context(), &plmv2.FollowRequest{JobId: id, Attempt: 1, Offset: from, Snapshot: true}, out.send))
	return &out
}

// ask is one read of jobID's log at that run and offset, and what it got.
func ask(t *testing.T, h *harness, jobID string, attempt int32, from int64, snapshot bool) (string, error) {
	t.Helper()
	var out collector
	err := h.follow(t.Context(), &plmv2.FollowRequest{JobId: jobID, Attempt: attempt, Offset: from, Snapshot: snapshot}, out.send)
	return out.text(), err
}

func TestAFollowIsProxiedToTheBuilderWritingTheLog(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	b := hold(t, h, "cloning\n")
	require.Equal(t, h.builderID.String(), h.mustRecord(t, id).Builder, "the record names where the run is")

	var out collector
	done := make(chan error, 1)
	go func() {
		done <- h.follow(t.Context(), &plmv2.FollowRequest{JobId: id, Attempt: 1}, out.send)
	}()
	out.waitText("cloning\n")
	b.say("compiling\n")
	out.waitText("cloning\ncompiling\n")

	b.finish()
	require.NoError(t, <-b.ended)
	require.NoError(t, <-done, "the run sealed is a complete answer")
	require.Equal(t, []plmv2.JobStatus{plmv2.JobStatus_JobStatusClaimed, plmv2.JobStatus_JobStatusSuccess}, out.statuses())

	var whole collector
	stored := h.assets.events(t, h.mustRecord(t, id).Logs[stamp(1)])
	for _, ev := range stored {
		whole.send(ev)
	}
	require.Equal(t, whole.last(), out.last(), "the offset a resuming client sends back is carried per event")
}

func TestFollowResumesAtAnOffset(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	b := hold(t, h, "first\nsecond\n")
	defer b.finish()

	whole := snapshotOf(t, h, id, 0)
	require.Equal(t, "first\nsecond\n", whole.text())
	require.Equal(t, "second\n", snapshotOf(t, h, id, whole.after(1)).text())
	require.Empty(t, snapshotOf(t, h, id, whole.last()).text(), "nothing has been written past that offset yet")
}

func TestFollowRefusesABadRequestByName(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	for name, req := range map[string]*plmv2.FollowRequest{
		"no job":              {Attempt: 1},
		"not a job id":        {JobId: "not-a-job-id", Attempt: 1},
		"a negative offset":   {JobId: id, Attempt: 1, Offset: -1},
		"no run":              {JobId: id},
		"a run past the next": {JobId: id, Attempt: 3},
	} {
		err := h.follow(t.Context(), req, (&collector{}).send)
		require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), name)
	}

	err := h.follow(t.Context(), &plmv2.FollowRequest{JobId: strings.Repeat("ab", 32), Attempt: 1}, (&collector{}).send)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "no such job")

	// An offset past the end of a log being written is the caller's arithmetic.
	b := hold(t, h, "cloning\n")
	defer b.finish()
	err = h.follow(t.Context(), &plmv2.FollowRequest{JobId: id, Attempt: 1, Offset: 4096}, (&collector{}).send)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestFollowEndsWhenTheViewerGoesAway(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	b := hold(t, h, "started\n")
	defer b.finish()

	ctx, cancel := context.WithCancel(t.Context())
	var out collector
	done := make(chan error, 1)
	go func() { done <- h.follow(ctx, &plmv2.FollowRequest{JobId: id, Attempt: 1}, out.send) }()
	out.waitText("started\n")

	cancel()
	b.say("still going\n")
	require.NoError(t, <-done, "a viewer hanging up is not a failure of the build")
}

func TestFollowStopsWhenItCannotSend(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	b := hold(t, h, "output\n")
	defer b.finish()

	broken := &collector{fail: errors.New("client went away")}
	require.ErrorContains(t, h.follow(t.Context(), &plmv2.FollowRequest{JobId: id, Attempt: 1}, broken.send), "client went away")
}

func TestAFollowOfARunToComeSaysTheJobIsQueuedAndWaits(t *testing.T) {
	h := newHarnessCfg(t, func(c *Config) { c.RecordReadInterval = time.Millisecond })
	id := h.planned(t, testRef, testCommit)

	var snap collector
	require.NoError(t, h.follow(t.Context(), &plmv2.FollowRequest{JobId: id, Attempt: 1, Snapshot: true}, snap.send))
	require.Equal(t, []plmv2.JobStatus{plmv2.JobStatus_JobStatusOpen}, snap.statuses(), "a snapshot of a run to come is the job queued")

	var out collector
	following := make(chan error, 1)
	go func() {
		following <- h.follow(t.Context(), &plmv2.FollowRequest{JobId: id, Attempt: 1}, out.send)
	}()
	out.waitStatus(plmv2.JobStatus_JobStatusOpen)

	b := hold(t, h, "cloning\n")
	out.waitText("cloning\n")
	b.finish()
	require.NoError(t, <-b.ended)
	require.NoError(t, <-following)
	require.Equal(t, []plmv2.JobStatus{
		plmv2.JobStatus_JobStatusOpen, plmv2.JobStatus_JobStatusClaimed, plmv2.JobStatus_JobStatusSuccess,
	}, out.statuses())
}

func TestAWaitingFollowEndsWithAJobCancelledBeforeItRan(t *testing.T) {
	h := newHarnessCfg(t, func(c *Config) { c.RecordReadInterval = time.Millisecond })
	id := h.planned(t, testRef, testCommit)

	var out collector
	following := make(chan error, 1)
	go func() {
		following <- h.follow(t.Context(), &plmv2.FollowRequest{JobId: id, Attempt: 1}, out.send)
	}()
	out.waitStatus(plmv2.JobStatus_JobStatusOpen)

	require.NoError(t, h.cancel(id))
	err := <-following
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	require.ErrorContains(t, err, plmv2.JobStatus_JobStatusCancelled.String())

	ctx, stop := context.WithCancel(t.Context())
	stop()
	other := h.planned(t, testRef, strings.Repeat("2", 40))
	require.NoError(t, h.follow(ctx, &plmv2.FollowRequest{JobId: other, Attempt: 1}, (&collector{}).send),
		"a follow whose viewer left while it waited ends quietly")
}

func TestAnyNodeHoldingTheRecordAnswersAFollow(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	b := hold(t, h, "cloning\n")
	defer b.finish()

	h.store.stepDown()
	got, err := ask(t, h, id, 1, 0, true)
	require.NoError(t, err, "a follower reads the record from its own replica and proxies as the leader would")
	require.Equal(t, "cloning\n", got)
	h.store.stepUp()
}

func TestAFollowOfABuilderThatCannotBeReachedIsUnavailable(t *testing.T) {
	h := newHarness(t)
	id := jobIDOf(t, testCommit)
	stranger := newHost(t, newMocknet(t)).ID()
	for name, builders := range map[string]Builders{"one it cannot reach": oneBuilder{id: h.builderID, runs: h.onBuilder}, "none": nil} {
		h.builders = builders
		err := h.proxy(t.Context(), stranger.String(), &plmv2.FollowRequest{JobId: id, Attempt: 1}, (&collector{}).send)
		require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err), name)
	}
	err := h.proxy(t.Context(), "not a peer id", &plmv2.FollowRequest{JobId: id, Attempt: 1}, (&collector{}).send)
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

// stalls is a builder whose Follow is refused with err, or streams events that end
// with err.
type stalls struct {
	run.Builder
	refuse error
	end    error
}

func (s stalls) Follow(context.Context, *plmv2.FollowRequest) (run.Events, error) {
	if s.refuse != nil {
		return nil, s.refuse
	}
	return &endsWith{err: s.end}, nil
}

// endsWith is a log of one event, then err.
type endsWith struct {
	sent bool
	err  error
}

func (e *endsWith) Receive() (*plmv2.Job_Event, error) {
	if !e.sent {
		e.sent = true
		return &plmv2.Job_Event{Kind: &plmv2.Job_Event_Message{Message: "a line"}}, nil
	}
	return nil, e.err
}

func (e *endsWith) Close() error { return nil }

func TestABuilderThatFailsAFollowIsSaidSo(t *testing.T) {
	h := newHarness(t)
	id := newHost(t, newMocknet(t)).ID()
	for name, tc := range map[string]struct {
		builder stalls
		code    connect.Code
	}{
		"refused":         {stalls{refuse: errors.New("gone")}, connect.CodeUnavailable},
		"refused by name": {stalls{refuse: connect.NewError(connect.CodeInvalidArgument, errors.New("offset"))}, connect.CodeInvalidArgument},
		"cut short":       {stalls{end: errors.New("the stream broke")}, connect.CodeUnavailable},
	} {
		h.builders = oneBuilder{id: id, runs: tc.builder}
		var out collector
		err := h.proxy(t.Context(), id.String(), &plmv2.FollowRequest{JobId: jobIDOf(t, testCommit), Attempt: 1}, out.send)
		require.Equal(t, tc.code, connect.CodeOf(err), name)
	}
}

func TestAnEndedRunIsTheRecordsToSay(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	b := hold(t, h, "cloning\n")
	b.finish()
	require.NoError(t, <-b.ended)
	h.letGo(t)
	for _, snapshot := range []bool{false, true} {
		_, err := ask(t, h, id, 1, 0, snapshot)
		require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "ended, snapshot=%v", snapshot)
		require.ErrorContains(t, err, plmv2.JobStatus_JobStatusSuccess.String())
	}
	_, err := ask(t, h, id, 2, 0, false)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "a finished job has no next run to wait for")

	h.records.garble(t, id)
	_, err = ask(t, h, id, 1, 0, false)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err), "a record this node cannot read")
}

func TestAResumeIsNeverAnotherRunsLog(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	h.builder.run = func(_ context.Context, _ run.Work, out run.Output) error {
		fmt.Fprint(out, "first run, line one\nfirst run, line two\n")
		return errors.New("the node went away")
	}
	require.True(t, h.mustStep(t))
	first := h.assets.events(t, h.mustRecord(t, id).Logs[stamp(1)])
	read := first[1].GetOffset()

	require.NoError(t, h.retry(id))
	b := hold(t, h, "second run, line one\nsecond run, line two\n")
	defer b.finish()
	require.Equal(t, uint32(2), h.mustRecord(t, id).Attempt)

	for _, snapshot := range []bool{false, true} {
		var out collector
		err := h.follow(t.Context(), &plmv2.FollowRequest{JobId: id, Attempt: 1, Offset: read, Snapshot: snapshot}, out.send)
		require.Empty(t, out.text(), "snapshot=%v: the second run's events are not the rest of the first run's log", snapshot)
		require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "snapshot=%v", snapshot)
	}

	got, err := ask(t, h, id, 2, 0, true)
	require.NoError(t, err)
	require.Equal(t, "second run, line one\nsecond run, line two\n", got)
}

func TestARunWhoseBuilderHasNotOpenedItsLogIsWaitedFor(t *testing.T) {
	h := newHarnessCfg(t, func(c *Config) { c.RecordReadInterval = time.Millisecond })
	id := h.planned(t, testRef, testCommit)
	rec := h.mustRecord(t, id)
	rec.Status, rec.Attempt, rec.Holder, rec.Builder = int32(plmv2.JobStatus_JobStatusClaimed), 1, h.host.ID().String(), h.builderID.String()
	h.plantRecord(t, rec)

	var out collector
	following := make(chan error, 1)
	go func() {
		following <- h.follow(t.Context(), &plmv2.FollowRequest{JobId: id, Attempt: 1}, out.send)
	}()
	out.waitStatus(plmv2.JobStatus_JobStatusOpen)

	_, err := h.onBuilder.Build(t.Context(), run.Work{JobID: id, Attempt: 1, Environment: testEnvironment, ProjectID: "shop"})
	require.NoError(t, err)
	out.waitStatus(plmv2.JobStatus_JobStatusClaimed)
	_, err = h.onBuilder.Seal(t.Context(), &builderv2.Sealing{Run: &builderv2.Run{JobId: id, Attempt: 1}, Status: plmv2.JobStatus_JobStatusSuccess})
	require.NoError(t, err)
	require.NoError(t, <-following)
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
	h.builder.run = func(_ context.Context, _ run.Work, out run.Output) error {
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
	require.Equal(t, plmv2.JobStatus_JobStatusSuccess, h.status(t, id), "the record settles before its log is stored")

	for _, snapshot := range []bool{true, false} {
		var out collector
		require.NoError(t, h.follow(t.Context(), &plmv2.FollowRequest{JobId: id, Attempt: 1, Snapshot: snapshot}, out.send), "snapshot=%v", snapshot)
		require.Equal(t, "cloning\ncompiling\n", out.text(), "snapshot=%v", snapshot)
		require.Equal(t, []plmv2.JobStatus{plmv2.JobStatus_JobStatusClaimed, plmv2.JobStatus_JobStatusSuccess}, out.statuses())
	}

	release()
	require.NoError(t, <-done)
	stored := h.assets.events(t, h.mustRecord(t, id).Logs[stamp(1)])
	require.Equal(t, "cloning\ncompiling\n", messagesOf(stored))
	h.letGo(t)
	_, err := ask(t, h, id, 1, 0, true)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "stored and named, and let go by the builder")
}

func TestACancelledRunsLogIsFollowedUntilItIsStored(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	h.builder.run = func(_ context.Context, _ run.Work, out run.Output) error {
		fmt.Fprint(out, "the first run\n")
		return errors.New("flaky")
	}
	require.True(t, h.mustStep(t))
	require.NoError(t, h.retry(id))

	started := make(chan struct{})
	h.builder.run = func(ctx context.Context, _ run.Work, out run.Output) error {
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
	require.Equal(t, plmv2.JobStatus_JobStatusCancelled, rec.status())
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
	require.Equal(t, plmv2.JobStatus_JobStatusCancelled, statusesOf(stored)[len(statusesOf(stored))-1],
		"and the log ends with the job's cancel")
	h.letGo(t)
	_, err := ask(t, h, id, 2, 0, false)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
}
