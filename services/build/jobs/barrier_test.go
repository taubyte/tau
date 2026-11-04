package jobs

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
)

// The barrier budget, asserted: a Barrier is a replicated raft log entry, the
// claim loop runs forever, and reconcile walks every open job.

// spent runs f and reports how many barriers it took.
func spent(h *harness, f func()) int64 {
	before := h.store.barriers.Load()
	f()
	return h.store.barriers.Load() - before
}

func TestWhatTheSectionsBarriersCost(t *testing.T) {
	// An idle cycle is the floor, paid once per ClaimInterval.
	t.Run("an idle claim cycle", func(t *testing.T) {
		h := newHarness(t)
		n := spent(h, func() {
			worked, err := h.step(t.Context())
			require.NoError(t, err)
			require.False(t, worked)
		})
		t.Logf("barriers=%d", n)
		require.Equal(t, int64(3), n,
			"the dead letter's claim, the sweep, and the work queue's claim — all pkg/queue's own")
	})

	// A claimed, built and settled job costs five more: begin's lead(), then settle's
	// four (the ack's section, the ack, the record's section, and the section filing
	// its log). The log handoff to the asset store sits between the last two, outside
	// jobLock, after the record, so the log ends with what the run left the job as.
	t.Run("a cycle that claims, runs and settles a job", func(t *testing.T) {
		h := newHarness(t)
		id := h.planned(t, testRef, testCommit)

		n := spent(h, func() { require.True(t, h.mustStep(t)) })
		t.Logf("barriers=%d", n)
		require.Equal(t, int64(8), n,
			"the three above, plus begin's lead(), settle's three lead()s and settle's ack")
		require.Equal(t, buildv2.JobStatus_JobStatusSuccess, h.status(t, id))
	})

	// The dead-letter drain's own section costs one more, on a path that only
	// runs when a job has spent every delivery it had.
	t.Run("a dead-letter drain", func(t *testing.T) {
		h := newHarness(t)
		id := h.planned(t, testRef, testCommit)
		exhaust(t, h)

		n := spent(h, func() {
			drained, err := h.drainDeadLetter()
			require.NoError(t, err)
			require.True(t, drained)
		})
		t.Logf("barriers=%d", n)
		require.Equal(t, int64(3), n, "the claim, the section's lead(), and the ack")
		require.Equal(t, buildv2.JobStatus_JobStatusFailed, h.status(t, id))
	})

	// Why mend's barrier is on the delete branch: a pass over live jobs is one barrier
	// for the whole walk, not one per open job.
	t.Run("a reconcile pass over open jobs", func(t *testing.T) {
		h := newHarness(t)
		const open = 16
		for i := range open {
			h.push(t, testRef, shaOf(i))
		}
		require.Len(t, h.open(t, testRepo), open)

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
			require.Equal(t, buildv2.JobStatus_JobStatusOpen, h.status(t, id))
		}
		h.clock.advance(h.cfg.Queue.VisibilityTimeout + h.cfg.BuildTimeout + time.Minute)

		n := spent(h, func() { require.NoError(t, h.reconcile()) })
		ready, _ := h.work.Depth()
		t.Logf("openJobs=%d repaired=%d barriers=%d", open, ready, n)
		require.Equal(t, open, ready)
		require.Equal(t, int64(1+open), n, "one for the pass, and one per push it made")
	})

	// The delete branch costs nothing more: the rows say which decision wrote them,
	// so dropping one needs no barrier.
	t.Run("a reconcile pass over orphaned rows", func(t *testing.T) {
		h := newHarness(t)
		const orphans = 4
		for i := range orphans {
			h.plant(t, testRepo, jobIDOf(t, shaOf(i)))
		}
		require.Len(t, h.open(t, testRepo), orphans)

		n := spent(h, func() { require.NoError(t, h.reconcile()) })
		t.Logf("orphans=%d barriers=%d", orphans, n)
		require.Empty(t, h.open(t, testRepo))
		require.Equal(t, int64(1), n, "one for the pass, however many rows it dropped")
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

// A HELD FOLLOW COSTS NO BARRIER PER RING: it takes one when it starts waiting and
// one when it stops, however often the leader's runs ring while it waits.
func TestAHeldFollowTakesNoBarrierPerRing(t *testing.T) {
	cost := func(rings int) int64 {
		h := newHarness(t)
		id := h.planned(t, testRef, testCommit)
		return spent(h, func() {
			var out collector
			following := make(chan error, 1)
			go func() {
				following <- h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Attempt: 1}, out.send)
			}()
			out.waitStatus(buildv2.JobStatus_JobStatusOpen)
			for range rings {
				h.runs.ring()
			}
			require.NoError(t, h.cancel(id))
			<-following
		})
	}
	quiet, rung := cost(0), cost(5)
	t.Logf("barriers quiet=%d rung=%d", quiet, rung)
	require.Equal(t, quiet, rung)
}
