package backend

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/services/build/backend/row"
)

// One test per pair of transitions that can be in flight at once: each reads the
// record, decides, and writes, and raft orders entries without seeing that the
// second decided on a stale read. The gate is on the read, which is where the
// window opens, and holds the gated transition for a bounded window since a
// correctly parked racer sends no signal.

// window is how long a gate holds a transition after its read: long enough for
// an unsynchronised sibling to read AND write, so that a lost update is
// reproduced rather than merely made likely.
const window = 100 * time.Millisecond

// readGatedStore fires a hook after a watched key's read answers. It counts reads
// because a section that triages on one read and decides on a second is only
// raced at the second; it holds several arms for passes over several rows.
type readGatedStore struct {
	*fakeStore

	mu      sync.Mutex
	key     string
	arms    []*gateArm
	written func()
}

// gateArm is one armed hook: which keys it watches, which read of them it fires
// on, and how many it has seen.
type gateArm struct {
	match func(string) bool
	after int
	seen  int
	hook  func()
}

func gated(t *testing.T, h *harness, key string) *readGatedStore {
	t.Helper()
	g := &readGatedStore{fakeStore: h.store, key: key}
	h.Service.store = g
	return g
}

// gatedHarness puts the gate under the queue too, since New opens the queue over
// the store it was given.
func gatedHarness(t *testing.T, opts ...Option) (*harness, *readGatedStore) {
	t.Helper()
	store := newStore()
	g := &readGatedStore{fakeStore: store}
	return newHarnessOver(t, g, store, opts...), g
}

// on arms the gate: the next read of its key runs racer on a goroutine of its
// own and holds the reader for a window, which is the interleaving.
func (s *readGatedStore) on(t *testing.T, racer func()) <-chan struct{} {
	t.Helper()
	return s.arm(func(key string) bool { return key == s.key }, 1, racer)
}

// onRead arms the gate on the nth read of one key. It is what a section with a
// triage read needs: the read the writes are decided on is the LAST one, and a
// racer released at the first is racing nothing.
func (s *readGatedStore) onRead(t *testing.T, key string, nth int, racer func()) <-chan struct{} {
	t.Helper()
	return s.arm(func(k string) bool { return k == key }, nth, racer)
}

// onFirstUnder arms the gate on the first read of ANY key under prefix, which
// is how a test gets between two writes that live in two different keyspaces —
// a section's read of a job record and the queue write that read decided. The
// queue's own keys are the only handle a test outside pkg/queue has on the
// instant its ack begins.
func (s *readGatedStore) onFirstUnder(t *testing.T, prefix string, racer func()) <-chan struct{} {
	t.Helper()
	return s.arm(func(key string) bool { return strings.HasPrefix(key, prefix) }, 1, racer)
}

func (s *readGatedStore) arm(match func(string) bool, after int, racer func()) <-chan struct{} {
	done := make(chan struct{})
	s.mu.Lock()
	defer s.mu.Unlock()
	s.arms = append(s.arms, &gateArm{match: match, after: after, hook: func() {
		go func() {
			defer close(done)
			racer()
		}()
		time.Sleep(window)
	}})
	return done
}

// onWritten arms the gate on the ENTRY THAT WRITES its key rather than on a
// read of it, for a section whose stale decision comes after the record already
// exists — create's push is the one such site, because there is nothing for a
// racer to name until the record has landed.
func (s *readGatedStore) onWritten(t *testing.T, racer func()) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	s.mu.Lock()
	defer s.mu.Unlock()
	s.written = func() {
		go func() {
			defer close(done)
			racer()
		}()
		time.Sleep(window)
	}
	return done
}

func (s *readGatedStore) Batch(ops []raft.BatchOp, timeout time.Duration) error {
	if err := s.fakeStore.Batch(ops, timeout); err != nil {
		return err
	}

	s.mu.Lock()
	hook := s.written
	if hook != nil && writes(ops, s.key) {
		s.written = nil
	} else {
		hook = nil
	}
	s.mu.Unlock()

	if hook != nil {
		hook()
	}
	return nil
}

func writes(ops []raft.BatchOp, key string) bool {
	for _, op := range ops {
		if op.Set != nil && op.Set.Key == key {
			return true
		}
	}
	return false
}

func (s *readGatedStore) Get(key string) ([]byte, bool) {
	value, found := s.fakeStore.Get(key)

	s.mu.Lock()
	var hook func()
	for i, a := range s.arms {
		if !a.match(key) {
			continue
		}
		if a.seen++; a.seen == a.after {
			hook = a.hook
			s.arms = append(s.arms[:i], s.arms[i+1:]...)
		}
		break
	}
	s.mu.Unlock()

	if hook != nil {
		hook()
	}
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
	h.runner.run = func(ctx context.Context, _ Work, _ Output) error {
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
	cancelled := gated(t, h, jobRecordKey(id)).on(t, func() { cancelErr = h.cancel(id) })

	ran := make(chan error, 1)
	go func() { ran <- h.run(context.Background(), d) }()

	<-cancelled
	require.NoError(t, <-ran)

	requireCancelKept(t, h, id, cancelErr)
	require.ErrorIs(t, <-stopped, context.Canceled,
		"the cancel must stop the run it raced, not leave it to its own deadline")
}

func TestACancelIsNotLostToTheSettleThatOverlapsIt(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	// The state settle runs in: the record Claimed by this node, the delivery
	// still held, the build finished cleanly.
	d, err := h.work.Claim()
	require.NoError(t, err)
	claim := h.mustRecord(t, id)
	claim.Status = int32(buildv2.JobStatus_JobStatusClaimed)
	value, err := claim.encode()
	require.NoError(t, err)
	claimed := row.Sized(1)
	claimed.Set(jobRecordKey(id), value)
	require.NoError(t, claimed.Apply(h.store, time.Second))

	var cancelErr error
	cancelled := gated(t, h, jobRecordKey(id)).on(t, func() { cancelErr = h.cancel(id) })

	require.NoError(t, h.settle(t.Context(), d, nil, nil))
	<-cancelled

	requireCancelKept(t, h, id, cancelErr)
}

func TestACancelIsNotLostToTheDeadLetterDrainThatOverlapsIt(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)
	exhaust(t, h)

	var cancelErr error
	cancelled := gated(t, h, jobRecordKey(id)).on(t, func() { cancelErr = h.cancel(id) })

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

	// The reopen this node holds committed and has not applied, built now and
	// planted the instant mend's triage read of it has answered.
	reopened := h.mustRecord(t, ahead)
	reopened.Status = int32(buildv2.JobStatus_JobStatusOpen)
	reopened.UpdatedAt = h.clock.now().Unix()
	value, err := reopened.encode()
	require.NoError(t, err)

	var plantErr error
	planted := gate.onRead(t, jobRecordKey(ahead), 1, func() {
		if plantErr = h.store.MockCluster.Set(jobRecordKey(ahead), value, time.Second); plantErr != nil {
			return
		}
		if plantErr = h.store.MockCluster.Set(openIndexKey(testRepo, ahead), nil, time.Second); plantErr != nil {
			return
		}
		plantErr = h.work.Push(ahead, nil)
	})

	// And the reopen that overlaps, released at the read the delete is decided
	// on. Armed at the first read instead, this races the triage and the
	// re-read catches it, which is a test of the re-read wearing the section's
	// name.
	var retryErr error
	retried := gate.onRead(t, jobRecordKey(raced), 2, func() { retryErr = h.retry(raced) })

	require.NoError(t, h.reconcile())
	<-planted
	require.NoError(t, plantErr, "the entry this pass had not applied")

	// A racer the gate never released is a failure: mend no longer makes the read this
	// was armed on.
	select {
	case <-retried:
		require.NoError(t, retryErr, "the caller was told its retry was accepted")
	case <-time.After(5 * time.Second):
		t.Log("mend took no second read of the raced row, so nothing was released into its window")
	}

	rows := h.store.Keys(openIndexPrefix(testRepo))
	ready, _ := h.work.Depth()
	t.Logf("raced=%s ahead=%s openRows=%d ready=%d",
		h.status(t, raced), h.status(t, ahead), len(rows), ready)

	require.Equal(t, buildv2.JobStatus_JobStatusOpen, h.status(t, raced))
	require.Equal(t, buildv2.JobStatus_JobStatusOpen, h.status(t, ahead))
	require.ElementsMatch(t,
		[]string{openIndexKey(testRepo, raced), openIndexKey(testRepo, ahead)}, rows,
		"a reopened job must be in the open index, or nothing will ever look at it again")
	require.Equal(t, 2, ready, "and each must have an item")
}

// finishedAndStillListed is a job that has finished with its open-index row
// left behind — the row mend exists to drop, and the row a reopen writes back.
// Its item is drained first, so the reopen's push is a real one.
func finishedAndStillListed(t *testing.T, h *harness, commit string) string {
	t.Helper()

	id := h.planned(t, testRef, commit)
	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.work.Ack(d))
	require.NoError(t, h.finishNow(t, id, buildv2.JobStatus_JobStatusFailed, "boom"))
	h.store.put(t, openIndexKey(testRepo, id), nil)
	return id
}

func TestAWithdrawnItemIsNotPutBackByTheDeliveryThatOverlapsIt(t *testing.T) {
	// THE PUSH'S OWN JOB, the plan, since a second delivery of the push is what
	// overlaps the cancel.
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	var cancelErr error
	cancelled := gated(t, h, jobRecordKey(id)).on(t, func() { cancelErr = h.cancel(id) })

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
	cancelled := gated(t, h, jobRecordKey(id)).onWritten(t, func() { cancelErr = h.cancel(id) })

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
