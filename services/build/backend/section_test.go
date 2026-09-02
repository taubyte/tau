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
// WRITTEN INTO THE REAL CODE, EVERY FORM BELOW ONCE PASSED EVERY STATIC GUARD
// IN THIS PACKAGE — several were, and did, with the whole suite green. Each
// reads a job record inside a section and makes the write that read decided
// from a position that is, textually, inside it; and each makes that write when
// the section is no longer held.
//
// MOST OF THEM CANNOT BE WRITTEN ANY MORE, and that is the point of the round
// that produced this file: there is no Enter to leave and retake, no accessor
// that hands out a live section, and no way to stamp a value with one. What is
// left is the one escape Go cannot express away — A HANDLE THAT OUTLIVES THE
// CALL IT WAS HANDED TO, captured, deferred, or sent to another goroutine — and
// the forms below are that one escape, in each of the shapes the earlier
// guards were defeated by. They are refused at runtime, because the handle is
// dead from the instant withJob returns.
//
// They are exercised against the real Service, the real store and the real
// jobLock, so that what is measured is the mechanism rather than a model of it,
// and each has the name of the shape it stands for so that a failure says which
// escape happened.

// escapee is a handle that outlived its call. It is the one thing a body can do
// that no type can stop it doing, so it is what every form below is built from.
func escapee(t *testing.T, h *harness, id string) *held {
	t.Helper()
	var out *held
	require.NoError(t, h.withJob(id, func(j *held) error {
		require.True(t, j.found(), "job %s must exist", id)
		out = j
		return nil
	}))
	return out
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
func TestAWriteMadeByTheBodyThatReadItIsAllowed(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	require.NoError(t, h.withJob(id, func(j *held) error {
		return j.finish(buildv1.JobStatus_JobStatusCancelled, "cancelled")
	}))
	require.Equal(t, buildv1.JobStatus_JobStatusCancelled, h.status(t, id))
}

// A HANDLE ASSEMBLED RATHER THAN HANDED OVER IS DEAD, AND THAT IS WHY FORGING
// ONE BUYS NOTHING.
//
// held is this package's own struct, so this package can of course build one —
// no scan and no type can stop a file assembling a struct declared beside it,
// and the round before this one spent its whole budget on making a token that
// could not be forged. THE AUTHORITY IS NOT THE STRUCT. It is the holding
// inside it, which only backend/section can make live and only inside a running
// Do. So a handle built anywhere else carries the zero holding, and every write
// through it is refused — while a handle built INSIDE a live body carries
// exactly the authority that body already had, which is not an escalation.
func TestAHandleAssembledOutsideAHoldingIsDead(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	forged := &held{s: h.Service, id: id}
	requireRefused(t, h, id, forged.commit(nil))
	requireRefused(t, h, id, forged.push())
	requireRefused(t, h, id, forged.withdraw())
	requireRefused(t, h, id, forged.register(func() {}))
	requireRefused(t, h, id, forged.stopRun())
	requireRefused(t, h, id, forged.finish(buildv1.JobStatus_JobStatusCancelled, "cancelled"))
	require.ErrorIs(t, forged.reread(), section.ErrStale,
		"and it cannot read itself a value to write on either")

	require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id))
}

// A GOROUTINE THAT HOLDS NOTHING CAN READ AND HAS NOTHING TO WRITE WITH. This
// is the shape that outlived the number: with a "which section is running"
// accessor, a goroutine holding NO LOCK AT ALL read the record, worked for ten
// milliseconds, wrote, and the record moved — the write was made under a live
// section that the writer did not hold.
//
// There is nothing for it to read now. Held() answers the question with a bool,
// withJob is the only thing that makes a handle, and it hands it to its own
// body — so the write is refused however long this waits and whoever is holding
// the lock meanwhile.
func TestAGoroutineThatHoldsNothingHasNothingToWriteWith(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	done := make(chan error, 1)
	entered := make(chan struct{})
	go func() {
		<-entered
		rec, found, err := h.readRecord(id)
		if err != nil || !found {
			done <- err
			return
		}
		// The ten milliseconds of real work the old shape did between the read
		// and the write, in which anything at all may write this record.
		time.Sleep(10 * time.Millisecond)
		// And the only handle this goroutine can put that record behind.
		done <- (&held{s: h.Service, id: rec.ID}).finish(buildv1.JobStatus_JobStatusCancelled, "cancelled")
	}()

	require.NoError(t, h.withJob(id, func(*held) error {
		require.True(t, jobLock.Held(), "a section IS running — and that is all it can learn")
		close(entered)
		return nil
	}))

	requireRefused(t, h, id, <-done)
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id),
		"and the record the sectionless read decided on must not have moved")
}

// FORM 1 — LEAVE AND RE-ENTER. The body read under the lock, released it, did
// real work, retook it and then wrote. Every call sat inside a section; it was
// not the section the read was taken in, and the six lines that put this shape
// back into begin() shipped with the whole suite green and the behavioural
// needle printing sectionHeldAtTheAck=true, because a needle that asks whether
// the lock is held finds the RE-TAKEN one.
//
// THERE IS NO LONGER AN Enter TO CALL, so what is left of the shape is a handle
// from a finished section spent inside a later one. It answers the needle
// exactly as the original did — the lock IS held at the write — and it is
// refused, because the handle names the section it came from and that section
// has returned.
func TestAHandleSpentInsideALaterSectionIsRefused(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	stale := escapee(t, h, id)

	// The real work the section was released for, and the window in which
	// anything at all may write this record.
	h.lastCommit(testRepo, testRef)

	var err error
	var heldAtTheWrite bool
	require.NoError(t, h.withJob(id, func(*held) error {
		heldAtTheWrite = jobLock.Held()
		err = stale.finish(buildv1.JobStatus_JobStatusCancelled, "cancelled")
		return nil
	}))

	require.True(t, heldAtTheWrite, "the lock is held at the write, which is exactly what the old needle asked")
	requireRefused(t, h, id, err)
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id),
		"and the record the stale read decided on must not have moved")
}

// FORM 2 — A DEFERRED WRITE. Registered outside the body, so it runs AFTER the
// section is over while its call site sits above it. drainDeadLetter rewritten
// this way passed everything and left jobLock free at the ack in ten runs out
// of ten.
func TestAWriteFromACallDeferredAfterTheSectionHasNoSectionAtAll(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	var err error
	func() {
		var j *held
		defer func() { err = j.finish(buildv1.JobStatus_JobStatusCancelled, "cancelled") }()

		require.NoError(t, h.withJob(id, func(in *held) error {
			j = in
			return nil
		}))
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
	require.NoError(t, h.withJob(id, func(j *held) error {
		pending = func() error { return j.finish(buildv1.JobStatus_JobStatusCancelled, "cancelled") }
		return nil
	}))

	requireRefused(t, h, id, pending())
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id))
}

// FORM 4 — A WRITE IN ANOTHER FUNCTION. It takes the handle or it cannot write
// at all, and that is now a parameter list rather than a scan: a helper with no
// *held has no route to Batch, to a queue write or to the running map.
//
// This is the form a table keyed by the READING function could not see at all,
// because the function that writes reads nothing. It is also the hand-off the
// design wants: what a body passes on is its own decision, and it dies with the
// section.
func TestAWriteInAnotherFunctionTakesTheHandleOrIsRefused(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	// The helper. It reads nothing and decides nothing; it is handed a handle
	// and writes what that handle's read decided.
	elsewhere := func(j *held) error {
		return j.finish(buildv1.JobStatus_JobStatusCancelled, "cancelled")
	}

	requireRefused(t, h, id, elsewhere(escapee(t, h, id)))
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id))

	require.NoError(t, h.withJob(id, elsewhere), "and the same call from inside is allowed")
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

	stale := escapee(t, h, id)

	// The write's own section: live, entered by the writer, and not the one the
	// handle came from.
	inADifferentSection := func(write func() error) error {
		var err error
		require.NoError(t, h.withJob(id, func(*held) error {
			require.True(t, jobLock.Held())
			err = write()
			return nil
		}))
		return err
	}

	t.Run("push", func(t *testing.T) {
		requireRefused(t, h, id, inADifferentSection(stale.push))
	})
	t.Run("withdraw", func(t *testing.T) {
		requireRefused(t, h, id, inADifferentSection(stale.withdraw))
		ready, claimed := h.work.Depth()
		require.Equal(t, 1, ready+claimed, "the item a refused withdrawal did not remove is still there")
	})
	t.Run("ack", func(t *testing.T) {
		d, err := h.work.Claim()
		require.NoError(t, err)
		requireRefused(t, h, id, inADifferentSection(func() error { return stale.ack(h.work, d) }))

		_, claimed := h.work.Depth()
		require.Equal(t, 1, claimed,
			"AND THE ITEM SURVIVES. An ack is the one queue write with nothing behind it: it "+
				"deletes the very index row a concurrent push deduplicates against, so an ack made "+
				"on a decision taken in a section that has closed destroys an item the caller was "+
				"told it had. Refused, the delivery stays claimed and comes back.")
	})
	t.Run("register", func(t *testing.T) {
		requireRefused(t, h, id, inADifferentSection(func() error { return stale.register(func() {}) }))
		h.runningMu.Lock()
		defer h.runningMu.Unlock()
		require.Empty(t, h.running,
			"AND NOTHING IS REGISTERED. A registration is not an entry in the log, and that is "+
				"why it was the last write with no checked path: a cancel serialized after the "+
				"claim finds what this map says, and a stale registration is a build nobody can "+
				"stop.")
	})
}

// A STOP CARRIED OUT AFTER ITS SECTION KILLS A BUILD SOMEBODY ELSE STARTED, and
// that is the harm rather than the shape.
//
// stopRun was a write with no door at all until this round. It reads the
// running map and INVOKES a context.CancelFunc — it reaches no store, so the
// enumeration of who WRITES that map walked past it, and the whole of its
// safety was the sentence "it is called inside cancel's section". Moved one
// line out, the entire suite stayed green.
//
// WHAT IT COSTS IS BELOW, deterministically and with no race to win: a cancel's
// decision, carried out after its section has returned, reaching the run a
// LATER section registered. The later run is the retry's — the job was
// cancelled and asked for again, which is two ordinary RPCs — and killing it
// ends it Failed("context canceled") having never built anything.
func TestAStopCarriedOutOfItsSectionCannotKillALaterRun(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	// The cancel's handle, escaped from the section that decided the stop.
	stale := escapee(t, h, id)

	// And the run a later section registered, exactly as begin does.
	killed := make(chan struct{})
	require.NoError(t, h.withJob(id, func(j *held) error {
		return j.register(func() { close(killed) })
	}))

	requireRefused(t, h, id, stale.stopRun())
	select {
	case <-killed:
		require.FailNow(t, "a stop made after its section killed a build a later section registered")
	default:
	}

	// And the section that DOES own the decision still stops it, so what was
	// refused is the stale carrying-out and not the stop.
	require.NoError(t, h.withJob(id, func(j *held) error { return j.stopRun() }))
	<-killed
}

// THE HANDLE DIES BEFORE THE EXCLUSION IS RELEASED, which is the ordering that
// leaves no instant in which a write could find a section already on its way
// out. jobLock.mu.Unlock() was once a thing this package could write: it
// released the mutual exclusion while the identity stayed live, and the static
// scan — which matched a selector whose X is the ident jobLock — saw one clean
// section. There is no mutex here to unlock any more, and nothing but Do
// touches the one there is.
func TestTheHandleDiesBeforeTheExclusionIsReleased(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	stale := escapee(t, h, id)
	require.False(t, jobLock.Held(), "the exclusion is released when the body returns")

	// A second goroutine gets in immediately, which is what "the exclusion was
	// released" means — and the handle from before is dead, which is what "the
	// section ended" means. The old shape had the first without the second.
	entered, wrote := make(chan struct{}), make(chan error, 1)
	go func() {
		wrote <- h.withJob(id, func(*held) error {
			close(entered)
			return stale.finish(buildv1.JobStatus_JobStatusCancelled, "cancelled")
		})
	}()
	<-entered

	requireRefused(t, h, id, <-wrote)
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id))
}

// THE LIVENESS IS NOT THIS PACKAGE'S TO ASSIGN, AND THAT IS THE COMPILER'S
// DOING RATHER THAN A TEST'S. Two rounds ago `r.at = at` re-stamped a read
// taken in a closed section with the section running now, and retry split into
// a reopen and a queueRetry that way ran green. One round ago the fields were
// unexported in another package and the STAMP was exported instead, so a body
// could put a live section on any value at all.
//
// There is neither now: section.Holding's fields are unexported, and the only
// thing that fills them in is Do, which takes the read itself. What is asserted
// here is the SHAPE THAT MAKES THAT TRUE, because the assignment that would
// prove it cannot be compiled.
func TestTheHoldingIsNotThisPackagesToAssign(t *testing.T) {
	typ := reflect.TypeOf(section.Holding[jobRecord]{})
	require.Equal(t, "github.com/taubyte/tau/services/build/backend/section", typ.PkgPath(),
		"a holding must be declared where this package cannot assign its fields")
	require.Positive(t, typ.NumField())
	for i := range typ.NumField() {
		require.False(t, typ.Field(i).IsExported(),
			"section.Holding.%s is exported, so a write site can stamp itself with a live section",
			typ.Field(i).Name)
	}

	// And the one holding this package CAN construct is the zero one, which is
	// live in nothing.
	var none section.Holding[jobRecord]
	require.ErrorIs(t, none.Write(func() error { return nil }), section.ErrStale)
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

// THE COST, MEASURED. A section is a mutex acquisition and two atomic stores,
// one atomic store on the way out, and one atomic load plus a nil check per
// write. The numbers are printed rather than asserted, because what they have
// to be smaller than is a raft round trip and no test in this package can bound
// one.
func BenchmarkSection(b *testing.B) {
	b.Run("a plain mutex", func(b *testing.B) {
		var mu sync.Mutex
		for b.Loop() {
			mu.Lock()
			mu.Unlock()
		}
	})
	b.Run("a section", func(b *testing.B) {
		var l section.Lock[jobRecord]
		read := func() (jobRecord, bool, error) { return jobRecord{}, true, nil }
		body := func(section.Holding[jobRecord]) error { return nil }
		for b.Loop() {
			if err := l.Do(read, body); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("the check a write makes", func(b *testing.B) {
		var l section.Lock[jobRecord]
		write := func() error { return nil }
		if err := l.Do(
			func() (jobRecord, bool, error) { return jobRecord{}, true, nil },
			func(h section.Holding[jobRecord]) error {
				for b.Loop() {
					if err := h.Write(write); err != nil {
						b.Fatal(err)
					}
				}
				return nil
			},
		); err != nil {
			b.Fatal(err)
		}
	})
}
