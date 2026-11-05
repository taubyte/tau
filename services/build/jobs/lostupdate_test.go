package jobs

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/services/build/run"
)

// One test per pair of transitions that can be in flight at once: each reads the
// job, decides, and appends, and nothing in the store sees that the second decided
// on a stale read. The gate is on the read, which is where the
// window opens, and holds the gated transition for a bounded window since a
// correctly parked racer sends no signal.

// window is how long a gate holds a transition after its read: long enough for
// an unsynchronised sibling to read AND write, so that a lost update is
// reproduced rather than merely made likely.
const window = 100 * time.Millisecond

// readGate fires a hook after a watched read answers: a key of the queue's raft
// store, or a prefix of the job records listed. It counts reads because a
// section that triages on one read and decides on a second is only raced at the
// second; it holds several arms for passes over several rows.
type readGate struct {
	mu      sync.Mutex
	key     string
	arms    []*gateArm
	written func()
}

// gateArm is one armed hook: which reads it watches, which of them it fires on,
// and how many it has seen.
type gateArm struct {
	match func(string) bool
	after int
	seen  int
	hook  func()
}

// readGatedStore is the queue's raft store behind the gate.
type readGatedStore struct {
	*fakeStore
	*readGate
}

// gated arms a gate on the job records of a running harness. key is a job's
// event prefix: a read of the job is a listing of it.
func gated(t *testing.T, h *harness, key string) *readGate {
	t.Helper()
	g := &readGate{key: key}
	h.records.gated(g)
	return g
}

// gatedHarness puts the gate under the queue too, since New opens the queue over
// the store it was given.
func gatedHarness(t *testing.T, opts ...option) (*harness, *readGate) {
	t.Helper()
	store := newStore()
	g := &readGate{}
	h := newHarnessOver(t, readGatedStore{fakeStore: store, readGate: g}, store, opts...)
	h.records.gated(g)
	return h, g
}

// on arms the gate: the next read of its key runs racer on a goroutine of its
// own and holds the reader for a window, which is the interleaving.
func (g *readGate) on(t *testing.T, racer func()) <-chan struct{} {
	t.Helper()
	return g.arm(func(key string) bool { return key == g.key }, 1, racer)
}

// onRead arms the gate on the nth read of one key. It is what a section with a
// triage read needs: the read the writes are decided on is the LAST one, and a
// racer released at the first is racing nothing.
func (g *readGate) onRead(t *testing.T, key string, nth int, racer func()) <-chan struct{} {
	t.Helper()
	return g.arm(func(k string) bool { return k == key }, nth, racer)
}

// onFirstUnder arms the gate on the first read of ANY key under prefix, which
// is how a test gets between two writes that live in two different keyspaces —
// a section's read of a job and the queue write that read decided. The queue's
// own keys are the only handle a test outside pkg/queue has on the instant its
// ack begins.
func (g *readGate) onFirstUnder(t *testing.T, prefix string, racer func()) <-chan struct{} {
	t.Helper()
	return g.arm(func(key string) bool { return strings.HasPrefix(key, prefix) }, 1, racer)
}

func (g *readGate) arm(match func(string) bool, after int, racer func()) <-chan struct{} {
	done := make(chan struct{})
	g.mu.Lock()
	defer g.mu.Unlock()
	g.arms = append(g.arms, &gateArm{match: match, after: after, hook: func() {
		go func() {
			defer close(done)
			racer()
		}()
		time.Sleep(window)
	}})
	return done
}

// onWritten arms the gate on the DELTA THAT WRITES under its key rather than on
// a read of it, for a section whose stale decision comes after the job already
// exists — create's push is the one such site, because there is nothing for a
// racer to name until the created event has landed.
func (g *readGate) onWritten(t *testing.T, racer func()) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	g.mu.Lock()
	defer g.mu.Unlock()
	g.written = func() {
		go func() {
			defer close(done)
			racer()
		}()
		time.Sleep(window)
	}
	return done
}

// wrote is a delta applied, spelled as its rows.
func (g *readGate) wrote(rows []string) {
	g.mu.Lock()
	hook := g.written
	if hook != nil && slices.ContainsFunc(rows, func(row string) bool { return strings.HasPrefix(row, "set "+g.key) }) {
		g.written = nil
	} else {
		hook = nil
	}
	g.mu.Unlock()

	if hook != nil {
		hook()
	}
}

// read is a read answered.
func (g *readGate) read(key string) {
	g.mu.Lock()
	var hook func()
	for i, a := range g.arms {
		if !a.match(key) {
			continue
		}
		if a.seen++; a.seen == a.after {
			hook = a.hook
			g.arms = append(g.arms[:i], g.arms[i+1:]...)
		}
		break
	}
	g.mu.Unlock()

	if hook != nil {
		hook()
	}
}

func (s readGatedStore) Get(key string) ([]byte, bool) {
	value, found := s.fakeStore.Get(key)
	s.read(key)
	return value, found
}

// requireCancelKept asserts a cancel that answered the caller is the record's last
// word; a refusal (the job had finished) is the only other honest answer.
func requireCancelKept(t *testing.T, h *harness, id string, err error) {
	t.Helper()
	t.Logf("cancel=%v status=%s anchor=%q builds=%d",
		err, h.status(t, id), h.lastCommit(testRepo, testRef, testEnvironment), len(h.runner.seen()))
	if err != nil {
		require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err),
			"the only honest refusal is a job that had already finished")
		rec := h.mustRecord(t, id)
		require.True(t, rec.terminal(), "and then it really must have finished")
		require.NotEqual(t, buildv2.JobStatus_JobStatusCancelled, rec.status(),
			"a refusal means something else finished it")
		return
	}
	require.Equal(t, buildv2.JobStatus_JobStatusCancelled, h.status(t, id),
		"a cancel that answered nil must not be overwritten")
	require.Empty(t, h.lastCommit(testRepo, testRef, testEnvironment),
		"and a cancelled build must not advance the ref anchor")
}

func TestACancelIsNotLostToTheClaimThatOverlapsIt(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	d, err := h.work.Claim()
	require.NoError(t, err)

	// The build ends when it is stopped. The fallback bounds a failing run
	// rather than softening the check: reaching it IS the failure, because the
	// cancel was supposed to end this build and instead left it to its own
	// deadline an hour away.
	stopped := make(chan error, 1)
	h.runner.run = func(ctx context.Context, _ run.Work, _ run.Output) error {
		select {
		case <-ctx.Done():
			stopped <- ctx.Err()
			return ctx.Err()
		case <-time.After(2 * time.Second):
			stopped <- nil
			return nil
		}
	}

	var cancelErr error
	cancelled := gated(t, h, eventPrefix(id)).on(t, func() { cancelErr = h.cancel(id) })

	ran := make(chan error, 1)
	go func() { ran <- h.run(context.Background(), d, h.runnerID, h.onRunner) }()

	<-cancelled
	require.NoError(t, <-ran)

	requireCancelKept(t, h, id, cancelErr)
	require.ErrorIs(t, <-stopped, context.Canceled,
		"the cancel must stop the run it raced, not leave it to its own deadline")
}

func TestACancelIsNotLostToTheSettleThatOverlapsIt(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	// The state settle runs in: the job Claimed by this node, the delivery still
	// held, the build finished cleanly.
	d, err := h.work.Claim()
	require.NoError(t, err)
	claim := h.mustRecord(t, id)
	claim.Status = int32(buildv2.JobStatus_JobStatusClaimed)
	claim.Attempt = 1
	h.plantRecord(t, claim)

	var cancelErr error
	cancelled := gated(t, h, eventPrefix(id)).on(t, func() { cancelErr = h.cancel(id) })

	ran := ranOnTheRunner(t, h, id, 1, "built\n")
	require.NoError(t, h.settle(t.Context(), d, h.onRunner, ran, produced{attempt: 1}, nil))
	<-cancelled

	requireCancelKept(t, h, id, cancelErr)
}

func TestACancelIsNotLostToTheDeadLetterDrainThatOverlapsIt(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	exhaust(t, h)

	var cancelErr error
	cancelled := gated(t, h, eventPrefix(id)).on(t, func() { cancelErr = h.cancel(id) })

	drained, err := h.drainDeadLetter()
	require.NoError(t, err)
	require.True(t, drained)
	<-cancelled

	requireCancelKept(t, h, id, cancelErr)
}

// exhaust spends every delivery attempt the queue allows, so the item ends up
// in the dead letter with nobody holding it.
func exhaust(t *testing.T, h *harness) {
	t.Helper()
	for range h.cfg.Queue.MaxAttempts + 1 {
		if _, err := h.work.Claim(); err != nil {
			break
		}
		h.clock.advance(2 * h.cfg.Queue.VisibilityTimeout)
		require.NoError(t, h.work.Sweep())
	}
	ready, claimed := h.work.Depth()
	require.Zero(t, ready+claimed, "the work queue must be empty")
	dead, _ := h.deadLetter(t).Depth()
	require.Equal(t, 1, dead, "the job must be in the dead letter or this test means nothing")
}

func TestAReopenIsNotLostToTheReconcileThatOverlapsIt(t *testing.T) {
	h, gate := gatedHarness(t)

	raced := finishedAndStillListed(t, h, testCommit)
	ahead := finishedAndStillListed(t, h, shaOf(3))

	// Another node's reopen, landing the instant mend's read of the job has
	// answered: its event and its open row, one delta.
	var plantErr error
	planted := gate.onRead(t, eventPrefix(ahead), 1, func() {
		h.elsewhere(t, ahead, &buildv2.JobEvent{Kind: &buildv2.JobEvent_Retried{Retried: &buildv2.JobRetried{}}},
			func(at int64, writer string) string { return openRow(testRepo, ahead, at, writer) })
		plantErr = h.work.Push(ahead, nil)
	})

	// And this node's reopen, released at the read the drop is decided on: the
	// section holds the job's lock, so the retry waits for mend and then reopens.
	var retryErr error
	retried := gate.onRead(t, eventPrefix(raced), 1, func() { retryErr = h.retry(raced) })

	require.NoError(t, h.reconcile())
	<-planted
	<-retried
	require.NoError(t, plantErr)
	require.NoError(t, retryErr, "the caller was told its retry was accepted")

	ready, _ := h.work.Depth()
	t.Logf("raced=%s ahead=%s open=%v ready=%d",
		h.status(t, raced), h.status(t, ahead), h.open(t, testRepo), ready)

	require.Equal(t, buildv2.JobStatus_JobStatusOpen, h.status(t, raced))
	require.Equal(t, buildv2.JobStatus_JobStatusOpen, h.status(t, ahead))
	require.ElementsMatch(t, []string{raced, ahead}, h.open(t, testRepo),
		"a reopened job must have an open row, or nothing will ever look at it again")
	require.Equal(t, 2, ready, "and each must have an item")
}

// finishedAndStillListed is a job that has finished with an open row left behind,
// the row mend exists to drop.
// Its item is drained first, so the reopen's push is a real one.
func finishedAndStillListed(t *testing.T, h *harness, commit string) string {
	t.Helper()

	id := h.planned(t, testRef, commit)
	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.work.Ack(d))
	require.NoError(t, h.finishNow(t, id, buildv2.JobStatus_JobStatusFailed, "boom"))
	h.plant(t, testRepo, id)
	return id
}

func TestAWithdrawnItemIsNotPutBackByTheDeliveryThatOverlapsIt(t *testing.T) {
	// THE PUSH'S OWN JOB, the plan, since a second delivery of the push is what
	// overlaps the cancel.
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	var cancelErr error
	cancelled := gated(t, h, eventPrefix(id)).on(t, func() { cancelErr = h.cancel(id) })

	_, err := h.enqueue(context.Background(), &buildv2.PushEvent{
		RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
	})
	require.NoError(t, err)
	<-cancelled
	require.NoError(t, cancelErr)

	ready, claimed := h.work.Depth()
	t.Logf("status=%s ready=%d claimed=%d", h.status(t, id), ready, claimed)

	require.Equal(t, buildv2.JobStatus_JobStatusCancelled, h.status(t, id))
	require.Zero(t, ready+claimed, "a cancelled job must not be left with work in the queue")
}

func TestANewJobIsNotLeftWithAnItemByTheCancelThatOverlapsItsCreation(t *testing.T) {
	h := newHarness(t)
	key, ok := parseRepositoryKey(testRepo)
	require.True(t, ok)
	id := jobID(key, testRef, testCommit)

	var cancelErr error
	cancelled := gated(t, h, eventPrefix(id)).onWritten(t, func() { cancelErr = h.cancel(id) })

	pushed, err := h.enqueue(context.Background(), &buildv2.PushEvent{
		RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
	})
	require.NoError(t, err)
	require.Equal(t, id, pushed)
	<-cancelled
	require.NoError(t, cancelErr)

	ready, claimed := h.work.Depth()
	t.Logf("status=%s ready=%d claimed=%d", h.status(t, id), ready, claimed)

	require.Equal(t, buildv2.JobStatus_JobStatusCancelled, h.status(t, id))
	require.Zero(t, ready+claimed, "a cancelled job must not be left with work in the queue")
}
