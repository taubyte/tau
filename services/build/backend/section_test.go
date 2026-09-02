package backend

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
	"github.com/taubyte/tau/services/build/backend/section"
)

// section_test.go is the half population_test.go cannot be.
//
// WRITTEN INTO THE REAL CODE, EVERY FORM BELOW PASSED EVERY STATIC GUARD IN
// THIS PACKAGE — several were, and did, with the whole suite green. Each reads
// a job record inside a jobLock section and makes the write that read decided
// from a position that is, textually, inside the same span; and each makes that
// write when the section is no longer held. A scan sees correct functions. The
// store sees writes decided on reads that something else was free to invalidate
// in between.
//
// AND THE LAST THREE ARE NEW IN KIND. The forms above are a write made when NO
// section is running; these are a write made while a section IS running that
// the writer does not hold — a goroutine that took no lock at all, an ack
// handed a live section that decided nothing, a registration built inside a
// section and made after it. A check that asks "is a section by this number
// running" answers yes to all three. That is the difference between recency and
// ownership, and it is why the token is now a value only Enter hands out.
//
// They are exercised against the real Service, the real store and the real
// jobLock, so that what is measured is the mechanism rather than a model of it,
// and each has the name of the shape it stands for so that a failure says which
// escape happened.

// inOneSection is what a correct transition does, and is the control every test
// below is measured against: one section, one read, and the write that read
// decided, made inside it.
func inOneSection(h *harness, id string, write func(reading) error) error {
	sec := jobLock.Enter()
	defer sec.Leave()

	r, err := h.record(sec, id)
	if err != nil {
		return err
	}
	return write(r)
}

// requireRefused is the shape of every failure below: the write did not happen,
// and the refusal says the section that decided it had closed.
func requireRefused(t *testing.T, h *harness, id string, err error) {
	t.Helper()
	t.Logf("write=%v status=%s", err, h.status(t, id))
	require.Error(t, err, "a write decided in a section that has closed must not be made")
	require.ErrorIs(t, err, section.ErrStale)
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

// A GOROUTINE THAT HOLDS NOTHING CAN ASK WHETHER A SECTION IS RUNNING AND
// CANNOT SPEND THE ANSWER. This is the shape that outlived the number: with a
// "which section is running" accessor, a goroutine holding NO LOCK AT ALL read
// the record, worked for ten milliseconds, wrote, and the record moved — the
// write was made under a live section that the writer did not hold.
//
// There is now nothing for it to read. Held() answers the question with a bool,
// Enter is the only thing that hands out a section, and the only reading this
// goroutine can take is stamped with none — so the write is refused however
// long it waits and whoever is holding the lock meanwhile.
func TestAGoroutineThatHoldsNoSectionCannotWriteWhatItRead(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	held := jobLock.Enter()
	defer held.Leave()
	require.True(t, jobLock.Held(), "a section IS running — and that is all it can learn")

	done := make(chan error, 1)
	go func() {
		r, err := h.record(unsectioned(), id)
		if err != nil {
			done <- err
			return
		}
		// The ten milliseconds of real work the old shape did between the read
		// and the write, in which anything at all may write this record.
		time.Sleep(10 * time.Millisecond)
		done <- h.finish(r, buildv1.JobStatus_JobStatusCancelled, "cancelled")
	}()

	requireRefused(t, h, id, <-done)
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id),
		"and the record the sectionless read decided on must not have moved")
}

// FORM 1 — LEAVE AND RE-ENTER. The body reads under the lock, releases it, does
// real work, retakes it and then writes. Every call sits inside a section; it is
// not the section the read was taken in, and the six lines that put this shape
// back into begin() shipped with the whole suite green and the behavioural
// needle printing sectionHeldAtTheAck=true, because a needle that asks whether
// the lock is held finds the RE-TAKEN one.
func TestASectionReleasedAndRetakenIsNotTheSectionThatRead(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	sec := jobLock.Enter()
	r, err := h.record(sec, id)
	require.NoError(t, err)
	require.True(t, r.Found())
	sec.Leave()

	// The real work the section was released for, and the window in which
	// anything at all may write this record.
	h.lastCommit(testRepo, testRef)

	next := jobLock.Enter()
	err = h.finish(r, buildv1.JobStatus_JobStatusCancelled, "cancelled")
	heldAtTheWrite := jobLock.Held()
	next.Leave()

	require.True(t, heldAtTheWrite, "the lock is held at the write, which is exactly what the old needle asked")
	requireRefused(t, h, id, err)
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id),
		"and the record the stale read decided on must not have moved")
}

// FORM 2 — A DEFERRED WRITE. Registered before the Leave defer, so LIFO runs it
// AFTER the section is over, while its call site sits inside it. drainDeadLetter
// rewritten this way passed everything and left jobLock free at the ack in ten
// runs out of ten.
func TestAWriteFromACallDeferredBeforeTheLeaveHasNoSectionAtAll(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	var err error
	func() {
		sec := jobLock.Enter()
		var r reading
		defer func() { err = h.finish(r, buildv1.JobStatus_JobStatusCancelled, "cancelled") }()
		defer sec.Leave()

		var read error
		r, read = h.record(sec, id)
		require.NoError(t, read)
		require.True(t, r.Found())
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
		sec := jobLock.Enter()
		defer sec.Leave()

		r, err := h.record(sec, id)
		require.NoError(t, err)
		require.True(t, r.Found())
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
// because the function that writes reads nothing. It is also the hand-off the
// design wants: what a holder passes on is its own decision, and it dies with
// the section.
func TestAWriteInAnotherFunctionCarriesTheSectionOrIsRefused(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	// The helper. It reads nothing and decides nothing; it is handed a reading
	// and writes what that reading decided.
	elsewhere := func(r reading) error {
		return h.finish(r, buildv1.JobStatus_JobStatusCancelled, "cancelled")
	}

	sec := jobLock.Enter()
	r, err := h.record(sec, id)
	require.NoError(t, err)
	sec.Leave()

	requireRefused(t, h, id, elsewhere(r))
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id))

	require.NoError(t, inOneSection(h, id, elsewhere), "and the same call from inside is allowed")
	require.Equal(t, buildv1.JobStatus_JobStatusCancelled, h.status(t, id))
}

// AND THE QUEUE WRITES ARE CHECKED THE SAME WAY, which matters because the
// defect that started all of this was a queue write and a record write drifting
// apart rather than two record writes racing.
//
// EACH IS MADE WHILE A DIFFERENT SECTION IS RUNNING, not merely while none is.
// That is the case a number could not refuse: it asked whether a section by
// this number was running, the answer was about the section the WRITER had just
// entered, and an ack handed one of those destroyed an item the caller had been
// told it had.
func TestTheQueueWritesAreRefusedInsideADifferentLiveSection(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	sec := jobLock.Enter()
	r, err := h.record(sec, id)
	require.NoError(t, err)
	sec.Leave()

	// The write's own section: live, entered by the writer, and not the one the
	// reading was taken in.
	inADifferentSection := func(write func() error) error {
		other := jobLock.Enter()
		defer other.Leave()
		require.True(t, jobLock.Held())
		return write()
	}

	t.Run("push", func(t *testing.T) {
		requireRefused(t, h, id, inADifferentSection(func() error { return h.Service.push(r, id) }))
	})
	t.Run("withdraw", func(t *testing.T) {
		requireRefused(t, h, id, inADifferentSection(func() error { return h.withdraw(r) }))
		ready, claimed := h.work.Depth()
		require.Equal(t, 1, ready+claimed, "the item a refused withdrawal did not remove is still there")
	})
	t.Run("ack", func(t *testing.T) {
		d, err := h.work.Claim()
		require.NoError(t, err)
		requireRefused(t, h, id, inADifferentSection(func() error { return h.ack(r, h.work, d) }))

		_, claimed := h.work.Depth()
		require.Equal(t, 1, claimed,
			"AND THE ITEM SURVIVES. An ack is the one queue write with nothing behind it: it "+
				"deletes the very index row a concurrent push deduplicates against, so an ack made "+
				"on a decision taken in a section that has closed destroys an item the caller was "+
				"told it had. Refused, the delivery stays claimed and comes back.")
	})
	t.Run("register", func(t *testing.T) {
		requireRefused(t, h, id, inADifferentSection(func() error {
			return h.register(r, func() {})
		}))
		h.runningMu.Lock()
		defer h.runningMu.Unlock()
		require.Empty(t, h.running,
			"AND NOTHING IS REGISTERED. A registration is not an entry in the log, and that is "+
				"the whole reason it was the last write with no checked path: a cancel serialized "+
				"after the claim finds what this map says, and a stale registration is a build "+
				"nobody can stop.")
	})
}

// A REGISTRATION IS A WRITE, AND ITS ONLY GUARD WAS A SCAN THAT COULD NOT SEE
// THE SPAN. s.running[id] = stop reaches no store, so the check could not see it
// and the AST table asked only whether the assignment sat inside SOME jobLock
// section — which a closure built inside the section and invoked after begin
// returns satisfies, textually, while running after the unlock.
//
// Handed the read instead, it is refused: begin's claim fails, the delivery
// stays claimed and comes back, and nothing is left half-registered.
func TestARegistrationBuiltInASectionAndMadeOutsideItIsRefused(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	stopped := make(chan struct{})
	var pending func() error
	func() {
		sec := jobLock.Enter()
		defer sec.Leave()

		r, err := h.record(sec, id)
		require.NoError(t, err)
		pending = func() error { return h.register(r, func() { close(stopped) }) }
	}()

	requireRefused(t, h, id, pending())

	h.runningMu.Lock()
	defer h.runningMu.Unlock()
	require.Empty(t, h.running, "a cancel serialized after that section would find nothing to stop")
}

// A READING CANNOT BE RE-STAMPED, AND THAT IS THE COMPILER'S DOING RATHER THAN
// A TEST'S. The guard before this scanned for composite literals of type
// reading, so `r.at = at` — a field assignment, not a literal — re-stamped a
// read taken in a closed section with the section running now, and Live()
// passed. retry split into a reopen and a queueRetry that way ran green.
//
// reading is section.Reading now: declared in another package, with every field
// unexported and no setter, so there is no assignment this package can write.
// What is asserted here is the SHAPE THAT MAKES THAT TRUE, because the
// assignment that would prove it cannot be compiled.
func TestAReadingIsImmutableBecauseItIsNotThisPackagesToAssign(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(reading{}),
		reflect.TypeOf(section.Section{}),
	} {
		require.Equal(t, "github.com/taubyte/tau/services/build/backend/section", typ.PkgPath(),
			"%s must be declared where this package cannot assign its fields", typ)
		require.Positive(t, typ.NumField())
		for i := range typ.NumField() {
			require.False(t, typ.Field(i).IsExported(),
				"%s.%s is exported, so a write site can stamp itself with a live section",
				typ, typ.Field(i).Name)
		}
	}

	// And the one Section value this package CAN construct is the zero one,
	// which is live in nothing.
	require.Error(t, unsectioned().Live())
	require.ErrorIs(t, unsectioned().Live(), section.ErrStale)
}

// LEAVING IS THE ONLY WAY TO RELEASE THE EXCLUSION, so the number cannot be
// left alive behind it. jobLock.mu.Unlock() was a thing this package could
// write: it released the mutual exclusion while the identity stayed live, and
// the static scan — which matched a selector whose X is the ident jobLock —
// saw one clean section. The mutex is in another package now and there is no
// method that touches it alone.
func TestReleasingTheExclusionAndEndingTheSectionAreOneAct(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	sec := jobLock.Enter()
	r, err := h.record(sec, id)
	require.NoError(t, err)
	require.True(t, jobLock.Held())

	sec.Leave()
	require.False(t, jobLock.Held(), "the identity cannot outlive the exclusion")

	// A second goroutine gets in immediately, which is what "the exclusion was
	// released" means — and the reading from before is dead, which is what
	// "the section ended" means. The old shape had the first without the
	// second.
	entered, wrote := make(chan struct{}), make(chan error, 1)
	go func() {
		next := jobLock.Enter()
		defer next.Leave()
		close(entered)
		wrote <- h.finish(r, buildv1.JobStatus_JobStatusCancelled, "cancelled")
	}()
	<-entered

	requireRefused(t, h, id, <-wrote)
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id))
}

// EVERY TRANSITION WRITES IN THE SECTION IT READ IN, driven end to end rather
// than assembled here — enqueue, a redelivery, the claim, the settle, a cancel,
// a retry, the dead-letter drain and a reconcile pass.
//
// It is the test that fails when any of the forms above is written into the
// real code: the transition that carries it has its write refused, and returns
// the refusal to this.
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

// AND THE CLAIM'S REGISTRATION IS ONE OF THEM, end to end: a cancel serialized
// after the claim stops the build, which it can only do if the run was
// registered inside the section that claimed it.
func TestACancelStopsTheRunTheClaimRegistered(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	// The build ends when it is stopped. The fallback bounds a failing run
	// rather than softening the check: reaching it IS the failure, because the
	// cancel was supposed to end this build and instead left it to its own
	// deadline an hour away.
	building, stopped := make(chan struct{}), make(chan error, 1)
	h.runner.run = func(ctx context.Context, _ Work, _ Sink) error {
		close(building)
		select {
		case <-ctx.Done():
			stopped <- ctx.Err()
			return ctx.Err()
		case <-time.After(2 * time.Second):
			stopped <- nil
			return nil
		}
	}

	ran := make(chan error, 1)
	go func() {
		_, err := h.step(context.Background())
		ran <- err
	}()
	<-building

	require.NoError(t, h.cancel(id))
	require.ErrorIs(t, <-stopped, context.Canceled)
	require.NoError(t, <-ran)
	require.Equal(t, buildv1.JobStatus_JobStatusCancelled, h.status(t, id))
}

// THE COST, MEASURED. A section entry is one atomic add and one atomic store on
// top of a mutex acquisition; an exit is one compare-and-swap; a write is one
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
		var l section.Lock
		for b.Loop() {
			l.Enter().Leave()
		}
	})
	b.Run("the check a write makes", func(b *testing.B) {
		sec := jobLock.Enter()
		defer sec.Leave()

		r, err := section.Read(sec, func() (jobRecord, bool, error) { return jobRecord{}, true, nil })
		if err != nil {
			b.Fatal(err)
		}
		for b.Loop() {
			if err := r.Live(); err != nil {
				b.Fatal(err)
			}
		}
	})
}
