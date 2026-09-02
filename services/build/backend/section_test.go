package backend

import (
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
)

// section_test.go is the half population_test.go cannot be.
//
// WRITTEN INTO THE REAL CODE, EVERY FORM BELOW PASSES EVERY STATIC GUARD IN
// THIS PACKAGE — three of them were, and did, with the whole suite green. Each
// reads a job record inside a jobLock section and makes the write that read
// decided from a position that is, textually, inside the same Lock..Unlock
// span; and each makes that write when the section is no longer held. A scan
// sees correct functions. The store sees writes decided on reads that something
// else was free to invalidate in between.
//
// They are exercised here against the real Service, the real store and the real
// jobLock, so that what is measured is the mechanism rather than a model of it,
// and each has the name of the shape it stands for so that a failure says which
// escape happened.

// inOneSection is what a correct transition does, and is the control every test
// below is measured against: one section, one read, and the write that read
// decided, made inside it.
func inOneSection(h *harness, id string, write func(reading) error) error {
	at := jobLock.Lock()
	defer jobLock.Unlock()

	r, err := h.record(at, id)
	if err != nil {
		return err
	}
	return write(r)
}

// requireRefused is the shape of every failure below: the write did not happen,
// and the reason names the two sections that did not match.
func requireRefused(t *testing.T, h *harness, id string, err error) {
	t.Helper()
	t.Logf("write=%v status=%s", err, h.status(t, id))
	require.Error(t, err, "a write decided in a section that has closed must not be made")
	require.ErrorContains(t, err, "decided under section")
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

// THE CONTROL. The same read and the same write, in one section, are allowed —
// so nothing below fails merely because the check refuses everything.
func TestAWriteMadeInTheSectionThatReadItIsAllowed(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	err := inOneSection(h, id, func(r reading) error {
		return h.finish(r, buildv1.JobStatus_JobStatusCancelled, "cancelled")
	})
	require.NoError(t, err)
	require.Equal(t, buildv1.JobStatus_JobStatusCancelled, h.status(t, id))
}

// TryLock IS AN ENTRY LIKE ANY OTHER, and that is why "is the lock held" was
// never the property. A needle that takes jobLock to find out whether a section
// is running is INSIDE A SECTION OF ITS OWN the instant it succeeds, and the
// section it is in is not the one that read.
func TestTryLockEntersASectionOfItsOwn(t *testing.T) {
	require.Equal(t, unsectioned, jobLock.current(), "no test may leave a section held")

	require.True(t, jobLock.TryLock())
	taken := jobLock.current()
	require.NotEqual(t, unsectioned, taken)
	require.NoError(t, taken.live())
	jobLock.Unlock()

	require.Equal(t, unsectioned, jobLock.current())
	require.Error(t, taken.live(), "and it is over the moment it is left")

	next := jobLock.Lock()
	require.False(t, jobLock.TryLock(), "a section that is running is not entered a second time")
	require.NotEqual(t, taken, next, "and the next entry is a different section, not the same one again")
	jobLock.Unlock()
}

// FORM 1 — UNLOCK AND RE-LOCK. The body reads under the lock, releases it, does
// real work, retakes it and then writes. Every call sits inside a
// Lock..Unlock span; it is not the span the read was taken in, and the six
// lines that put this shape back into begin() shipped with the whole suite
// green and the behavioural needle printing sectionHeldAtTheAck=true, because a
// needle that TryLocks at the write finds the RE-TAKEN lock held.
func TestASectionReleasedAndRetakenIsNotTheSectionThatRead(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	at := jobLock.Lock()
	r, err := h.record(at, id)
	require.NoError(t, err)
	require.True(t, r.found)
	jobLock.Unlock()

	// The real work the section was released for, and the window in which
	// anything at all may write this record.
	h.lastCommit(testRepo, testRef)

	jobLock.Lock()
	held := jobLock.TryLock()
	err = h.finish(r, buildv1.JobStatus_JobStatusCancelled, "cancelled")
	jobLock.Unlock()

	require.False(t, held, "the lock is held at the write, which is exactly what the old needle asked")
	requireRefused(t, h, id, err)
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id),
		"and the record the stale read decided on must not have moved")
}

// FORM 2 — A DEFERRED WRITE. Registered before the unlock defer, so LIFO runs
// it AFTER the unlock, while its call site sits inside the span. drainDeadLetter
// rewritten this way passed everything and left jobLock free at the ack in ten
// runs out of ten.
func TestAWriteFromACallDeferredBeforeTheUnlockHasNoSectionAtAll(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	var err error
	func() {
		at := jobLock.Lock()
		var r reading
		defer func() { err = h.finish(r, buildv1.JobStatus_JobStatusCancelled, "cancelled") }()
		defer jobLock.Unlock()

		var read error
		r, read = h.record(at, id)
		require.NoError(t, read)
		require.True(t, r.found)
	}()

	requireRefused(t, h, id, err)
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id))
}

// FORM 3 — A CLOSURE BUILT INSIDE AND INVOKED OUTSIDE. The write is written
// inside the section, which is where every scan looks, and called after it.
func TestAClosureBuiltInASectionAndInvokedOutsideItIsRefused(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	var pending func() error
	func() {
		at := jobLock.Lock()
		defer jobLock.Unlock()

		r, err := h.record(at, id)
		require.NoError(t, err)
		require.True(t, r.found)
		pending = func() error { return h.finish(r, buildv1.JobStatus_JobStatusCancelled, "cancelled") }
	}()

	requireRefused(t, h, id, pending())
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id))
}

// FORM 4 — A WRITE IN ANOTHER FUNCTION. It carries the identity or it cannot
// write, and the check says which: the same helper, handed the same reading,
// is allowed from inside the section that took it and refused from outside.
//
// This is the form a table keyed by the READING function could not see at all,
// because the function that writes reads nothing.
func TestAWriteInAnotherFunctionCarriesTheSectionOrIsRefused(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	// The helper. It reads nothing and decides nothing; it is handed a reading
	// and writes what that reading decided.
	elsewhere := func(r reading) error {
		return h.finish(r, buildv1.JobStatus_JobStatusCancelled, "cancelled")
	}

	at := jobLock.Lock()
	r, err := h.record(at, id)
	require.NoError(t, err)
	jobLock.Unlock()

	requireRefused(t, h, id, elsewhere(r))
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id))

	require.NoError(t, inOneSection(h, id, elsewhere), "and the same call from inside is allowed")
	require.Equal(t, buildv1.JobStatus_JobStatusCancelled, h.status(t, id))
}

// AND THE QUEUE WRITES ARE CHECKED THE SAME WAY, which matters because the
// defect that started all of this was a queue write and a record write drifting
// apart rather than two record writes racing.
func TestTheQueueWritesAreRefusedOutsideTheSectionThatDecidedThem(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	at := jobLock.Lock()
	r, err := h.record(at, id)
	require.NoError(t, err)
	jobLock.Unlock()

	t.Run("push", func(t *testing.T) {
		requireRefused(t, h, id, h.Service.push(r, id))
	})
	t.Run("withdraw", func(t *testing.T) {
		requireRefused(t, h, id, h.withdraw(r))
		ready, claimed := h.work.Depth()
		require.Equal(t, 1, ready+claimed, "the item a refused withdrawal did not remove is still there")
	})
	t.Run("ack", func(t *testing.T) {
		d, err := h.work.Claim()
		require.NoError(t, err)
		requireRefused(t, h, id, h.ack(r.at, h.work, d))

		_, claimed := h.work.Depth()
		require.Equal(t, 1, claimed,
			"AND THE ITEM SURVIVES. An ack is the one queue write with nothing behind it: it "+
				"deletes the very index row a concurrent push deduplicates against, so an ack made "+
				"on a decision taken in a section that has closed destroys an item the caller was "+
				"told it had. Refused, the delivery stays claimed and comes back.")
	})
}

// EVERY TRANSITION WRITES IN THE SECTION IT READ IN, driven end to end rather
// than assembled here — enqueue, a redelivery, the claim, the settle, a cancel,
// a retry, the dead-letter drain and a reconcile pass.
//
// It is the test that fails when any of the four forms above is written into
// the real code: the transition that carries it has its write refused, and
// returns the refusal to this.
func TestEveryTransitionWritesInTheSectionItReadIn(t *testing.T) {
	h := newHarness(t)

	// create, and then resume: the same push twice.
	id := h.push(t, testRef, testCommit)
	require.Equal(t, id, h.push(t, testRef, testCommit))

	// begin and settle.
	require.True(t, h.mustStep(t))
	require.Equal(t, buildv1.JobStatus_JobStatusSuccess, h.status(t, id))

	// retry, then cancel the reopened job.
	require.NoError(t, h.retry(id))
	require.NoError(t, h.cancel(id))
	require.Equal(t, buildv1.JobStatus_JobStatusCancelled, h.status(t, id))

	// the dead-letter drain.
	second := h.push(t, testRef, shaOf(1))
	exhaust(t, h)
	drained, err := h.drainDeadLetter()
	require.NoError(t, err)
	require.True(t, drained)
	require.Equal(t, buildv1.JobStatus_JobStatusFailed, h.status(t, second))

	// mend, both branches: an orphaned row to delete and a live one to leave.
	h.store.put(t, openIndexKey(testRepo, id), nil)
	third := h.push(t, testRef, shaOf(2))
	require.NoError(t, h.reconcile())
	require.Equal(t, []string{openIndexKey(testRepo, third)}, h.store.Keys(openIndexPrefix(testRepo)))
}

// THE COST, MEASURED. A section entry is one atomic add and one atomic store on
// top of a mutex acquisition; an exit is one atomic store; a write is one
// atomic load and a compare on top of a replicated raft entry. The numbers are
// printed rather than asserted, because what they have to be smaller than is a
// raft round trip and no test in this package can bound one.
func BenchmarkSection(b *testing.B) {
	b.Run("a plain mutex", func(b *testing.B) {
		var mu sync.Mutex
		for b.Loop() {
			mu.Lock()
			mu.Unlock()
		}
	})
	b.Run("a section", func(b *testing.B) {
		var l sectionLock
		for b.Loop() {
			l.Lock()
			l.Unlock()
		}
	})
	b.Run("the check a write makes", func(b *testing.B) {
		jobLock.Lock()
		defer jobLock.Unlock()
		at := jobLock.current()
		for b.Loop() {
			if err := at.live(); err != nil {
				b.Fatal(err)
			}
		}
	})
}
