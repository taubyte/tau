package backend

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/raft"
	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
	"github.com/taubyte/tau/services/build/backend/row"
)

// lostupdate_test.go is one test per PAIR of transitions that can be in flight
// at the same instant, because the defect it covers is in none of them on its
// own: every write of a job record reads the record, decides, and writes, and
// raft ORDERS two entries without noticing that the second was decided on a
// read taken before the first.
//
// THE GATE IS ON THE READ, not on a Batch. The window this closes is "the read
// and the write with nothing between them", so the only place a test can open
// it reliably is the read itself — a hook on "the next entry" fires on whatever
// queue write the transition happens to make first, which for the dead-letter
// drain is its own claim and for a redelivered push is the record it did not
// write. Gated there, those two reproduce nothing and would have shipped as
// tests that pass either way.
//
// The hook then holds the gated transition for a bounded window rather than
// waiting for a signal from the racing one, because when this is correct the
// racing one is parked and has no signal to send. That is
// TestASecondDeliveryCannotDecideWhileTheFirstIsWriting's shape, and
// pkg/v2/queue/exclusive_test.go's, for the same reason.

// window is how long a gate holds a transition after its read: long enough for
// an unsynchronised sibling to read AND write, so that a lost update is
// reproduced rather than merely made likely.
const window = 100 * time.Millisecond

// readGatedStore fires a hook when a key it is watching is read, after the read
// has answered. Everything else is the harness's store.
//
// IT COUNTS READS, because WHICH read a hook fires on is the difference between
// a test and a test that passes either way. A section that triages on a cheap
// read and decides on a second one is only raced by a hook on the SECOND: armed
// on the first, the racer it launches is caught by the re-read and the section
// is never reached at all — which is how PAIR 4 below came to pass two thousand
// runs against a mend with its jobLock section deleted.
//
// AND IT HOLDS SEVERAL ARMS AT ONCE, because a pass over several rows is where
// two racers of different kinds meet one walk.
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

// gatedHarness builds the harness with the gate ALREADY UNDER THE QUEUE, which
// gated() above cannot do: New opens the queue over the store it was handed, so
// a wrapper installed afterwards sees the service's reads and none of the
// queue's.
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
// queue's own keys are the only handle a test outside pkg/v2/queue has on the
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

// requireCancelKept is the invariant the cancel pairs assert: A CANCEL THAT
// ANSWERED THE CALLER IS THE RECORD'S LAST WORD. Anything else is a caller told
// the build stopped while it ran to completion and published its anchor.
//
// A refusal is the one other honest answer — the job had already finished — and
// is checked just as hard, so that "cancel returned an error" cannot be how a
// broken version passes.
func requireCancelKept(t *testing.T, h *harness, id string, err error) {
	t.Helper()
	t.Logf("cancel=%v status=%s anchor=%q builds=%d",
		err, h.status(t, id), h.lastCommit(testRepo, testRef), len(h.runner.seen()))
	if err != nil {
		require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err),
			"the only honest refusal is a job that had already finished")
		rec := h.mustRecord(t, id)
		require.True(t, rec.terminal(), "and then it really must have finished")
		require.NotEqual(t, buildv1.JobStatus_JobStatusCancelled, rec.status(),
			"a refusal means something else finished it")
		return
	}
	require.Equal(t, buildv1.JobStatus_JobStatusCancelled, h.status(t, id),
		"a cancel that answered nil must not be overwritten")
	require.Empty(t, h.lastCommit(testRepo, testRef),
		"and a cancelled build must not advance the ref anchor")
}

// PAIR 1 — cancel (an RPC goroutine) against run's claim (the loop's).
//
// The window is between run's re-read of the record and the entry that writes
// it back as Claimed. Unserialized, a cancel landing in it wrote Cancelled,
// could not withdraw an item a consumer holds, and found nothing to stop —
// nothing was registered until invoke had started — and then run's entry,
// decided on the read taken before all of that, put the record back to Claimed.
// The build ran to completion, the job reported Success, and the ref anchor
// advanced to the commit the caller cancelled.
func TestACancelIsNotLostToTheClaimThatOverlapsIt(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

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

// PAIR 2 — cancel against settle, the same loop goroutine one step later: the
// build is over, the delivery is being acked, the terminal record is being
// written. settle re-reads exactly so that a cancel is not overwritten, and the
// re-read is as stale as any other the moment something writes between it and
// the entry it decided.
func TestACancelIsNotLostToTheSettleThatOverlapsIt(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	// The state settle runs in: the record Claimed by this node, the delivery
	// still held, the build finished cleanly.
	d, err := h.work.Claim()
	require.NoError(t, err)
	claim := h.mustRecord(t, id)
	claim.Status = int32(buildv1.JobStatus_JobStatusClaimed)
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

// PAIR 3 — cancel against the dead-letter drain, the loop's third writer of a
// record. It marks an exhausted job Failed, and a cancel in the window is
// overwritten by that.
func TestACancelIsNotLostToTheDeadLetterDrainThatOverlapsIt(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
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

// PAIR 4 — a reopen against the reconcile pass that overlaps it.
//
// TWO ROWS IN ONE PASS, because mend has two guards and they stop two different
// racers. With one row this was a DISJUNCTION — it passed with EITHER guard
// present — and that is not a detail: it passed two thousand runs against a
// mend whose jobLock section had been deleted, leaving the section pinned by an
// AST table and nothing else. The gate fired on mend's FIRST read, which since
// the exemption was split is the TRIAGE read that decides nothing, so the retry
// it released was caught by the post-barrier re-read and the section was never
// reached.
//
//   - THE ROW THE SECTION DEFENDS (raced). reconcile reads a record, finds it
//     finished, and deletes the open-index row that says otherwise. A retry
//     answering on an rpc goroutine in the window between THAT read — the
//     second, the deciding one — and the delete reopens the job and writes that
//     very row back; mend's entry then deletes it. This racer CONTENDS FOR
//     jobLock, which is exactly what makes the section the thing that stops it.
//
//   - THE ROW THE RE-READ DEFENDS (ahead). The other racer cannot be raced,
//     because it has ALREADY HAPPENED. reconcile takes one barrier, at the top,
//     and lists behind it; the record is read per row, arbitrarily later, and a
//     node that regains leadership mid-walk holds entries that are committed
//     and not applied. The triage read sees a job as finished while the retry
//     that reopened it is already ordered ahead of anything this pass will
//     write — and raft.BatchOp has no compare-and-swap, so the delete lands on
//     the row that retry wrote. The gate plants the reopen after the triage
//     read has answered, which is what an unapplied entry looks like from
//     inside mend. Nothing contends for anything, and no section can help.
//
// NOTHING REPAIRS EITHER. reconcile walks the open index, so a job missing from
// it is a job reconcile will never look at again, and MaxOpenPerRepo stops
// counting it.
func TestAReopenIsNotLostToTheReconcileThatOverlapsIt(t *testing.T) {
	h, gate := gatedHarness(t)

	raced := finishedAndStillListed(t, h, testCommit)
	ahead := finishedAndStillListed(t, h, shaOf(3))

	// The reopen this node holds committed and has not applied, built now and
	// planted the instant mend's triage read of it has answered.
	reopened := h.mustRecord(t, ahead)
	reopened.Status = int32(buildv1.JobStatus_JobStatusOpen)
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

	// A RACER THE GATE NEVER RELEASED IS NOT A PASSING TEST. It means mend no
	// longer makes the read this was armed on — the triage and the deciding
	// read collapsed back into one — and mend then walked both rows with
	// nothing racing it at all. It is not waited out, because waiting out a
	// hang is how a test reports "nothing happened" as success; the two rows
	// below are what say what actually happened.
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

	require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, raced))
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, ahead))
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

	id := h.push(t, testRef, commit)
	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.work.Ack(d))
	require.NoError(t, h.finishNow(t, id, buildv1.JobStatus_JobStatusFailed, "boom"))
	h.store.put(t, openIndexKey(testRepo, id), nil)
	return id
}

// PAIR 5 — a redelivered webhook against cancel. Enqueue answers a push it
// already has by re-pushing the item, and that decision is a read of the
// record: a cancel that finishes the job and withdraws the item in the window
// has it put straight back, for a job nobody will ever build.
func TestAWithdrawnItemIsNotPutBackByTheDeliveryThatOverlapsIt(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	var cancelErr error
	cancelled := gated(t, h, jobRecordKey(id)).on(t, func() { cancelErr = h.cancel(id) })

	_, err := h.enqueue(context.Background(), &buildv1.PushEvent{
		RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
	})
	require.NoError(t, err)
	<-cancelled
	require.NoError(t, cancelErr)

	ready, claimed := h.work.Depth()
	t.Logf("status=%s ready=%d claimed=%d", h.status(t, id), ready, claimed)

	require.Equal(t, buildv1.JobStatus_JobStatusCancelled, h.status(t, id))
	require.Zero(t, ready+claimed, "a cancelled job must not be left with work in the queue")
}

// PAIR 6 — cancel against the push that finishes a job's creation. The job id
// is derived from (repository, ref, commit), so it is guessable before Enqueue
// has answered, and a Cancel naming it can arrive between the entry that writes
// the record and the item that entry is meant to be claimed by.
func TestANewJobIsNotLeftWithAnItemByTheCancelThatOverlapsItsCreation(t *testing.T) {
	h := newHarness(t)
	key, ok := parseRepositoryKey(testRepo)
	require.True(t, ok)
	id := jobID(key, testRef, testCommit)

	var cancelErr error
	cancelled := gated(t, h, jobRecordKey(id)).onWritten(t, func() { cancelErr = h.cancel(id) })

	pushed, err := h.enqueue(context.Background(), &buildv1.PushEvent{
		RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
	})
	require.NoError(t, err)
	require.Equal(t, id, pushed)
	<-cancelled
	require.NoError(t, cancelErr)

	ready, claimed := h.work.Depth()
	t.Logf("status=%s ready=%d claimed=%d", h.status(t, id), ready, claimed)

	require.Equal(t, buildv1.JobStatus_JobStatusCancelled, h.status(t, id))
	require.Zero(t, ready+claimed, "a cancelled job must not be left with work in the queue")
}
