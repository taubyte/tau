package backend

import (
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
)

// The one inconsistency this design can produce: a record written and a push
// that never landed. reconcile is what notices, and Push's own deduplication is
// its entire safety.
func TestReconcileReQueuesARecordWhosePushWasLost(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	// The state a crash between the record's entry and the queue's leaves.
	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.work.Ack(d))
	ready, _ := h.work.Depth()
	require.Equal(t, 0, ready)

	// Not yet: the age gate is what keeps this pass from issuing a barrier per
	// open job on every run.
	require.NoError(t, h.reconcile())
	ready, _ = h.work.Depth()
	require.Equal(t, 0, ready)

	h.clock.advance(h.cfg.Queue.VisibilityTimeout + h.cfg.BuildTimeout + time.Second)
	require.NoError(t, h.reconcile())

	ready, _ = h.work.Depth()
	require.Equal(t, 1, ready)
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id))
}

// IT CANNOT TOUCH A RUNNING JOB, and that is Push's own dedup rather than a
// check here: pushing an id the queue already holds — ready, delayed, backing
// off or claimed — is a no-op. v1's re-announce had no such property and
// re-queued jobs another cluster was already running.
func TestReconcileCannotDisturbAClaimedJob(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	d, err := h.work.Claim()
	require.NoError(t, err)

	h.clock.advance(h.cfg.Queue.VisibilityTimeout + h.cfg.BuildTimeout + time.Second)
	require.NoError(t, h.reconcile())

	ready, claimed := h.work.Depth()
	require.Equal(t, 0, ready)
	require.Equal(t, 1, claimed, "the claim must survive a reconcile")

	// And the delivery is still the live one.
	require.NoError(t, h.work.Ack(d))
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id))
}

// An open row whose record has finished, or has gone, is the row that is wrong.
// Dropping it is what keeps MaxOpenPerRepo counting real work rather than
// history.
func TestReconcileDropsStaleOpenRows(t *testing.T) {
	h := newHarness(t)

	finished := h.push(t, testRef, testCommit)
	require.NoError(t, h.finish(h.mustRecord(t, finished), buildv1.JobStatus_JobStatusSuccess, ""))
	// finish already removed the row; put it back to stand for a crash between
	// the two halves of some earlier transition.
	h.store.put(t, openIndexKey(testRepo, finished), nil)

	h.store.put(t, openIndexKey(testRepo, unknownJobID), nil)

	require.Len(t, h.store.Keys(openIndexRoot()), 2)
	require.NoError(t, h.reconcile())
	require.Empty(t, h.store.Keys(openIndexRoot()))
}

func TestReconcileIsLeaderOnly(t *testing.T) {
	h := newHarness(t)
	h.push(t, testRef, testCommit)
	h.store.stepDown()

	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(h.reconcile()))
}

func TestReconcileReportsWhatItCannotRead(t *testing.T) {
	t.Run("an index row that does not decode", func(t *testing.T) {
		h := newHarness(t)
		h.store.put(t, openIndexPrefix(testRepo)+"!!!", nil)

		require.Equal(t, connect.CodeInternal, connect.CodeOf(h.reconcile()))
	})

	t.Run("a record that does not decode", func(t *testing.T) {
		h := newHarness(t)
		id := h.push(t, testRef, testCommit)
		h.store.put(t, jobRecordKey(id), []byte("not cbor"))

		require.Equal(t, connect.CodeInternal, connect.CodeOf(h.reconcile()))
	})

	t.Run("a store that refuses the repair", func(t *testing.T) {
		h := newHarness(t)
		h.store.put(t, openIndexKey(testRepo, unknownJobID), nil)
		h.store.failBatches(errors.New("log is full"))

		require.Error(t, h.reconcile())
	})

	// The delete branch takes a barrier of its own, so it has a refusal of its
	// own — and it happens after reconcile's, which succeeded.
	t.Run("a store that refuses the delete branch its barrier", func(t *testing.T) {
		h := newHarness(t)
		h.store.put(t, openIndexKey(testRepo, unknownJobID), nil)
		h.store.onKeys(openIndexRoot(), func() {
			h.store.failBarrier(errors.New("lost quorum"))
		})

		require.Equal(t, connect.CodeUnavailable, connect.CodeOf(h.reconcile()))
		require.Len(t, h.store.Keys(openIndexRoot()), 1,
			"and nothing is deleted on a read this node could not make current")
	})

	// The second read is a read like any other, and a record that does not
	// decode is reported rather than treated as a record that is gone — which
	// would delete the row of a job whose record is merely corrupt.
	t.Run("a record that does not decode on the deciding read", func(t *testing.T) {
		h, stale := staleHarness(t)
		id := h.push(t, testRef, testCommit)
		d, err := h.work.Claim()
		require.NoError(t, err)
		require.NoError(t, h.work.Ack(d))
		require.NoError(t, h.finish(h.mustRecord(t, id), buildv1.JobStatus_JobStatusFailed, "boom"))

		applied, exists := stale.fakeStore.Get(jobRecordKey(id))
		require.True(t, exists)
		stale.fakeStore.put(t, openIndexKey(testRepo, id), nil)
		stale.fakeStore.put(t, jobRecordKey(id), []byte("not cbor"))
		stale.behindFrom(openIndexRoot(), jobRecordKey(id), applied)

		require.Equal(t, connect.CodeInternal, connect.CodeOf(h.reconcile()))
	})

	t.Run("a store that refuses the re-queue", func(t *testing.T) {
		h := newHarness(t)
		id := h.push(t, testRef, testCommit)
		d, err := h.work.Claim()
		require.NoError(t, err)
		require.NoError(t, h.work.Ack(d))

		h.clock.advance(h.cfg.Queue.VisibilityTimeout + h.cfg.BuildTimeout + time.Second)
		h.store.failBatches(errors.New("log is full"))

		require.ErrorContains(t, h.reconcile(), id)
	})
}

// The loop's other ticker. One test that it is wired, for the same reason the
// claim loop has one.
func TestTheStartedLoopReconciles(t *testing.T) {
	h := newHarness(t, func(s *Service) error {
		s.cfg.ClaimInterval = time.Hour // only reconcile may fire
		s.cfg.ReconcileEvery = time.Millisecond
		return nil
	})
	id := h.push(t, testRef, testCommit)

	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.work.Ack(d))
	h.clock.advance(h.cfg.Queue.VisibilityTimeout + h.cfg.BuildTimeout + time.Second)

	require.NoError(t, h.Start(t.Context()))
	require.Eventually(t, func() bool {
		ready, _ := h.work.Depth()
		return ready == 1
	}, 5*time.Second, time.Millisecond, "the reconcile ticker never re-queued job %s", id)
}

// staleHarness is a Service over a node that can be behind what it holds
// committed. The wrapper goes under New, so the queue is behind it too.
func staleHarness(t testing.TB) (*harness, *staleStore) {
	t.Helper()
	store := newStore()
	stale := &staleStore{fakeStore: store}
	return newHarnessOver(t, stale, store), stale
}

// mend'S DELETE IS DECIDED ON A READ, AND THE READ AND THE LISTING ABOVE IT
// COME FROM TWO DIFFERENT APPLIED PREFIXES.
//
// reconcile takes one barrier, at the top, and lists the open index behind it.
// The record is read per row, arbitrarily later, and a node that regains
// leadership mid-walk holds entries that are committed and not applied: it
// reads a job as finished while the retry that reopened it is already ordered
// ahead of anything this pass will write. raft.BatchOp has no compare-and-swap,
// so the delete lands on the row that retry wrote.
//
// reconcile.go names the harm: a job that reads Open and that nothing walking
// the open index will ever look at again — MaxOpenPerRepo stops counting it and
// no later pass can see it.
func TestMendDoesNotDropAnOpenRowItReadAtAnOlderPrefix(t *testing.T) {
	h, stale := staleHarness(t)
	id := h.push(t, testRef, testCommit)

	// What this node has APPLIED: the job lost its item and finished, so its
	// open row is gone.
	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.work.Ack(d))
	require.NoError(t, h.finish(h.mustRecord(t, id), buildv1.JobStatus_JobStatusFailed, "boom"))
	applied, exists := stale.fakeStore.Get(jobRecordKey(id))
	require.True(t, exists)

	// And what it holds committed and has not: a retry, which reopened the job
	// and wrote its open row back in one entry.
	require.NoError(t, h.retry(id))
	_, row := stale.fakeStore.Get(openIndexKey(testRepo, id))
	require.True(t, row, "the retry wrote the row this pass is about to walk")

	// The lag starts at the listing, which is where reconcile's own barrier is
	// already behind it.
	stale.behindFrom(openIndexRoot(), jobRecordKey(id), applied)

	n := spent(h, func() { require.NoError(t, h.reconcile()) })

	raw, exists := stale.fakeStore.Get(jobRecordKey(id))
	require.True(t, exists)
	rec, err := decodeJobRecord(jobRecordKey(id), raw)
	require.NoError(t, err)
	_, row = stale.fakeStore.Get(openIndexKey(testRepo, id))
	t.Logf("status=%s openRow=%v barriers=%d", rec.status(), row, n)

	require.Equal(t, buildv1.JobStatus_JobStatusOpen, rec.status(),
		"the retry was accepted, so the job is open")
	require.True(t, row,
		"and its open row must survive a pass whose record read predates the retry")
}
