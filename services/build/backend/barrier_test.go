package backend

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
)

// barrier_test.go SPENDS the barrier budget, because "every section takes its
// own" is a cost as well as a correctness argument: a Barrier is a replicated
// raft log entry, the claim loop runs on a ticker forever, and reconcile walks
// every open job in the cloud.
//
// The numbers below are the whole of the argument for where the barriers are
// and where they are not. They are asserted rather than printed so that a
// change to the budget has to be a change to this file.

// spent runs f and reports how many barriers it took.
func spent(h *harness, f func()) int64 {
	before := h.store.barriers.Load()
	f()
	return h.store.barriers.Load() - before
}

func TestWhatTheSectionsBarriersCost(t *testing.T) {
	// AN IDLE CYCLE IS THE FLOOR, and this service already pays it once per
	// ClaimInterval — a second by default — before any section of this
	// package's own is entered. Everything below is measured against it.
	t.Run("an idle claim cycle", func(t *testing.T) {
		h := newHarness(t)
		n := spent(h, func() {
			worked, err := h.step(t.Context())
			require.NoError(t, err)
			require.False(t, worked)
		})
		t.Logf("barriers=%d", n)
		require.Equal(t, int64(3), n,
			"the dead letter's claim, the sweep, and the work queue's claim — all pkg/v2/queue's own")
	})

	// ONE JOB CLAIMED, BUILT AND SETTLED COSTS TWO MORE, and only ONE of them
	// is new: begin's lead(). settle's is its own ack, which this path always
	// made. Per BUILD — minutes of work — against a loop that spends three a
	// second regardless.
	t.Run("a cycle that claims, runs and settles a job", func(t *testing.T) {
		h := newHarness(t)
		id := h.push(t, testRef, testCommit)

		n := spent(h, func() { require.True(t, h.mustStep(t)) })
		t.Logf("barriers=%d", n)
		require.Equal(t, int64(5), n,
			"the three above, plus begin's lead() and settle's ack")
		require.Equal(t, buildv1.JobStatus_JobStatusSuccess, h.status(t, id))
	})

	// The dead-letter drain's own section costs one more, on a path that only
	// runs when a job has spent every delivery it had.
	t.Run("a dead-letter drain", func(t *testing.T) {
		h := newHarness(t)
		id := h.push(t, testRef, testCommit)
		exhaust(t, h)

		n := spent(h, func() {
			drained, err := h.drainDeadLetter()
			require.NoError(t, err)
			require.True(t, drained)
		})
		t.Logf("barriers=%d", n)
		require.Equal(t, int64(3), n, "the claim, the section's lead(), and the ack")
		require.Equal(t, buildv1.JobStatus_JobStatusFailed, h.status(t, id))
	})

	// AND THIS IS WHY mend'S BARRIER IS ON THE DELETE BRANCH. A reconcile pass
	// over live jobs is one barrier for the whole walk, however many are open.
	// Taken before mend's first read instead, it would be one replicated entry
	// per open job per pass — the cost reconcile.go's age gate already exists to
	// avoid, and at MaxQueueDepth an order of magnitude more than the claim
	// loop's whole budget.
	t.Run("a reconcile pass over open jobs", func(t *testing.T) {
		h := newHarness(t)
		const open = 16
		for i := range open {
			h.push(t, testRef, shaOf(i))
		}
		require.Len(t, h.store.Keys(openIndexPrefix(testRepo)), open)

		n := spent(h, func() { require.NoError(t, h.reconcile()) })
		t.Logf("openJobs=%d barriers=%d", open, n)
		require.Equal(t, int64(1), n,
			"one for the pass. A barrier per row would be %d, every ReconcileEvery, forever", open)
	})

	// The age gate is what keeps that true once the jobs are old enough to be
	// re-queued: past the cutoff every row costs a push, and a push barriers.
	t.Run("a reconcile pass that actually repairs", func(t *testing.T) {
		h := newHarness(t)
		const open = 4
		for i := range open {
			id := h.push(t, testRef, shaOf(i))
			// A record with no item is what reconcile repairs. Draining the
			// item without acking the record is how one is made.
			d, err := h.work.Claim()
			require.NoError(t, err)
			require.NoError(t, h.work.Ack(d))
			require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id))
		}
		h.clock.advance(h.cfg.Queue.VisibilityTimeout + h.cfg.BuildTimeout + time.Minute)

		n := spent(h, func() { require.NoError(t, h.reconcile()) })
		ready, _ := h.work.Depth()
		t.Logf("openJobs=%d repaired=%d barriers=%d", open, ready, n)
		require.Equal(t, open, ready)
		require.Equal(t, int64(1+open), n, "one for the pass, and one per push it made")
	})

	// AND THIS IS WHAT THE DELETE BRANCH ACTUALLY COSTS: one per row that looks
	// orphaned, paid where the decision is made rather than on every row of
	// every pass. In steady state there are none of these, which is the whole
	// difference between the two placements — this number is bounded by the
	// repair work there is to do, and one-per-read is bounded by the size of the
	// cloud.
	t.Run("a reconcile pass over orphaned rows", func(t *testing.T) {
		h := newHarness(t)
		const orphans = 4
		for i := range orphans {
			h.store.put(t, openIndexKey(testRepo, jobIDOf(t, shaOf(i))), nil)
		}
		require.Len(t, h.store.Keys(openIndexPrefix(testRepo)), orphans)

		n := spent(h, func() { require.NoError(t, h.reconcile()) })
		t.Logf("orphans=%d barriers=%d", orphans, n)
		require.Empty(t, h.store.Keys(openIndexPrefix(testRepo)))
		require.Equal(t, int64(1+orphans), n,
			"one for the pass, and one for each row this pass actually deleted")
	})
}

// jobIDOf is the id a push of this commit would mint, for a test that needs a
// well-formed id without a record behind it.
func jobIDOf(t *testing.T, commit string) string {
	t.Helper()
	key, ok := parseRepositoryKey(testRepo)
	require.True(t, ok)
	return jobID(key, testRef, commit)
}

// shaOf is a distinct full object name per index, so each push is a different
// job rather than the same one deduplicated.
func shaOf(i int) string {
	return testCommit[:len(testCommit)-2] + string("0123456789abcdef"[i/16]) + string("0123456789abcdef"[i%16])
}
