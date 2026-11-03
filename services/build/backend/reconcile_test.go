package backend

import (
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
)

func TestReconcileReQueuesARecordWhosePushWasLost(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

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
	require.Equal(t, buildv2.JobStatus_JobStatusOpen, h.status(t, id))
}

func TestReconcileCannotDisturbAClaimedJob(t *testing.T) {
	h := newHarness(t)
	id := h.planned(t, testRef, testCommit)

	d, err := h.work.Claim()
	require.NoError(t, err)

	h.clock.advance(h.cfg.Queue.VisibilityTimeout + h.cfg.BuildTimeout + time.Second)
	require.NoError(t, h.reconcile())

	ready, claimed := h.work.Depth()
	require.Equal(t, 0, ready)
	require.Equal(t, 1, claimed, "the claim must survive a reconcile")

	// And the delivery is still the live one.
	require.NoError(t, h.work.Ack(d))
	require.Equal(t, buildv2.JobStatus_JobStatusOpen, h.status(t, id))
}

func TestReconcileDropsStaleOpenRows(t *testing.T) {
	h := newHarness(t)

	finished := h.planned(t, testRef, testCommit)
	require.NoError(t, h.finishNow(t, finished, buildv2.JobStatus_JobStatusSuccess, ""))
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
	h.planned(t, testRef, testCommit)
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
		id := h.planned(t, testRef, testCommit)
		h.store.put(t, jobRecordKey(id), []byte("not a record"))

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
		id := h.planned(t, testRef, testCommit)
		d, err := h.work.Claim()
		require.NoError(t, err)
		require.NoError(t, h.work.Ack(d))
		require.NoError(t, h.finishNow(t, id, buildv2.JobStatus_JobStatusFailed, "boom"))

		applied, exists := stale.fakeStore.Get(jobRecordKey(id))
		require.True(t, exists)
		stale.fakeStore.put(t, openIndexKey(testRepo, id), nil)
		stale.fakeStore.put(t, jobRecordKey(id), []byte("not a record"))
		stale.behindFrom(openIndexRoot(), jobRecordKey(id), applied)

		require.Equal(t, connect.CodeInternal, connect.CodeOf(h.reconcile()))
	})

	t.Run("a store that refuses the re-queue", func(t *testing.T) {
		h := newHarness(t)
		id := h.planned(t, testRef, testCommit)
		d, err := h.work.Claim()
		require.NoError(t, err)
		require.NoError(t, h.work.Ack(d))

		h.clock.advance(h.cfg.Queue.VisibilityTimeout + h.cfg.BuildTimeout + time.Second)
		h.store.failBatches(errors.New("log is full"))

		require.ErrorContains(t, h.reconcile(), id)
	})
}

func TestTheStartedLoopReconciles(t *testing.T) {
	h := newHarnessCfg(t, func(c *Config) {
		c.ClaimInterval = time.Hour // only reconcile may fire
		c.ReconcileEvery = time.Millisecond
	})
	id := h.planned(t, testRef, testCommit)

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

func TestMendDoesNotDropAnOpenRowItReadAtAnOlderPrefix(t *testing.T) {
	h, stale := staleHarness(t)
	id := h.planned(t, testRef, testCommit)

	// What this node has APPLIED: the job lost its item and finished, so its
	// open row is gone.
	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.work.Ack(d))
	require.NoError(t, h.finishNow(t, id, buildv2.JobStatus_JobStatusFailed, "boom"))
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
	rec, err := decodeJobRecord(jobRecordKey(id), id, raw)
	require.NoError(t, err)
	_, row = stale.fakeStore.Get(openIndexKey(testRepo, id))
	t.Logf("status=%s openRow=%v barriers=%d", rec.status(), row, n)

	require.Equal(t, buildv2.JobStatus_JobStatusOpen, rec.status(),
		"the retry was accepted, so the job is open")
	require.True(t, row,
		"and its open row must survive a pass whose record read predates the retry")
}
