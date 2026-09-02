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
