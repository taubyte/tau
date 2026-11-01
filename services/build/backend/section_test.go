package backend

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/services/build/backend/section"
)

// The runtime half population_test.go cannot be: a handle that outlives withJob
// (captured, deferred, sent to a goroutine), in each shape earlier guards were
// beaten by, against the real Service, store and jobLock. Each is refused.

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

func TestAWriteMadeByTheBodyThatReadItIsAllowed(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	require.NoError(t, h.withJob(id, func(j *held) error {
		return j.finish(buildv2.JobStatus_JobStatusCancelled, "cancelled")
	}))
	require.Equal(t, buildv2.JobStatus_JobStatusCancelled, h.status(t, id))
}

func TestAHandleAssembledOutsideAHoldingIsDead(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	forged := &held{s: h.Service}
	requireRefused(t, h, id, forged.writes().commit("forging"))
	requireRefused(t, h, id, forged.push())
	requireRefused(t, h, id, forged.withdraw())
	requireRefused(t, h, id, forged.register(func() {}))
	requireRefused(t, h, id, forged.stopRun())
	requireRefused(t, h, id, forged.finish(buildv2.JobStatus_JobStatusCancelled, "cancelled"))
	require.ErrorIs(t, forged.reread(), section.ErrStale,
		"and it cannot read itself a value to write on either")

	require.Equal(t, buildv2.JobStatus_JobStatusOpen, h.status(t, id))
}

func TestAGoroutineThatHoldsNothingHasNothingToWriteWith(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	done := make(chan error, 1)
	entered := make(chan struct{})
	go func() {
		<-entered
		_, found, err := h.readRecord(id)
		if err != nil || !found {
			done <- err
			return
		}
		// The ten milliseconds of real work the old shape did between the read
		// and the write, in which anything at all may write this record.
		time.Sleep(10 * time.Millisecond)
		// And the only handle this goroutine can put the record behind cannot be pointed
		// at the job it read: there is no id field.
		done <- (&held{s: h.Service}).finish(buildv2.JobStatus_JobStatusCancelled, "cancelled")
	}()

	require.NoError(t, h.withJob(id, func(*held) error {
		require.True(t, jobLock.Held(), "a section IS running — and that is all it can learn")
		close(entered)
		return nil
	}))

	requireRefused(t, h, id, <-done)
	require.Equal(t, buildv2.JobStatus_JobStatusOpen, h.status(t, id),
		"and the record the sectionless read decided on must not have moved")
}

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
		err = stale.finish(buildv2.JobStatus_JobStatusCancelled, "cancelled")
		return nil
	}))

	require.True(t, heldAtTheWrite, "the lock is held at the write, which is exactly what the old needle asked")
	requireRefused(t, h, id, err)
	require.Equal(t, buildv2.JobStatus_JobStatusOpen, h.status(t, id),
		"and the record the stale read decided on must not have moved")
}

func TestAWriteFromACallDeferredAfterTheSectionHasNoSectionAtAll(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	var err error
	func() {
		var j *held
		defer func() { err = j.finish(buildv2.JobStatus_JobStatusCancelled, "cancelled") }()

		require.NoError(t, h.withJob(id, func(in *held) error {
			j = in
			return nil
		}))
	}()

	requireRefused(t, h, id, err)
	require.Equal(t, buildv2.JobStatus_JobStatusOpen, h.status(t, id))
}

func TestAClosureBuiltInASectionAndInvokedOutsideItIsRefused(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	var pending func() error
	require.NoError(t, h.withJob(id, func(j *held) error {
		pending = func() error { return j.finish(buildv2.JobStatus_JobStatusCancelled, "cancelled") }
		return nil
	}))

	requireRefused(t, h, id, pending())
	require.Equal(t, buildv2.JobStatus_JobStatusOpen, h.status(t, id))
}

func TestAWriteInAnotherFunctionTakesTheHandleOrIsRefused(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	// The helper. It reads nothing and decides nothing; it is handed a handle
	// and writes what that handle's read decided.
	elsewhere := func(j *held) error {
		return j.finish(buildv2.JobStatus_JobStatusCancelled, "cancelled")
	}

	requireRefused(t, h, id, elsewhere(escapee(t, h, id)))
	require.Equal(t, buildv2.JobStatus_JobStatusOpen, h.status(t, id))

	require.NoError(t, h.withJob(id, elsewhere), "and the same call from inside is allowed")
	require.Equal(t, buildv2.JobStatus_JobStatusCancelled, h.status(t, id))
}

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
			return stale.finish(buildv2.JobStatus_JobStatusCancelled, "cancelled")
		})
	}()
	<-entered

	requireRefused(t, h, id, <-wrote)
	require.Equal(t, buildv2.JobStatus_JobStatusOpen, h.status(t, id))
}

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

func TestEveryTransitionWritesInTheSectionItReadIn(t *testing.T) {
	h := newHarness(t)

	// create, and then resume: the same push twice.
	id := h.push(t, testRef, testCommit)
	require.Equal(t, id, h.push(t, testRef, testCommit))

	// begin and settle.
	require.True(t, h.mustStep(t))
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, h.status(t, id))

	// retry, then cancel the reopened job.
	require.NoError(t, h.retry(id))
	require.NoError(t, h.cancel(id))
	require.Equal(t, buildv2.JobStatus_JobStatusCancelled, h.status(t, id))

	// the dead-letter drain.
	second := h.push(t, testRef, shaOf(1))
	exhaust(t, h)
	drained, err := h.drainDeadLetter()
	require.NoError(t, err)
	require.True(t, drained)
	require.Equal(t, buildv2.JobStatus_JobStatusFailed, h.status(t, second))

	// mend, both branches: an orphaned row to delete and a live one to leave.
	h.store.put(t, openIndexKey(testRepo, id), nil)
	third := h.push(t, testRef, shaOf(2))
	require.NoError(t, h.reconcile())
	require.Equal(t, []string{openIndexKey(testRepo, third)}, h.store.Keys(openIndexPrefix(testRepo)))
}

func TestACancelStopsTheRunTheClaimRegistered(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	// Reaching the fallback is the failure: the cancel should have ended the build.
	building, stopped := make(chan struct{}), make(chan error, 1)
	h.runner.run = func(ctx context.Context, _ Work, _ Output) error {
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
	require.Equal(t, buildv2.JobStatus_JobStatusCancelled, h.status(t, id))
}

// rowsOf is every row of one job, so "the neighbour did not move" is measured.
func rowsOf(h *harness, id string) map[string]string {
	out := map[string]string{}
	for _, key := range h.store.Keys(rootSegment + "/") {
		if !strings.Contains(key, seg(id)) {
			continue
		}
		value, _ := h.store.Get(key)
		out[key] = string(value)
	}
	return out
}

func TestAnEntryCannotNameAJobItsSectionWasNotEnteredOn(t *testing.T) {
	h := newHarness(t)
	a := h.push(t, testRef, testCommit)
	b := h.push(t, testRef, shaOf(1))
	require.NotEqual(t, a, b)

	before := rowsOf(h, b)
	require.NotEmpty(t, before, "the neighbour must have rows for this to measure anything")

	// Every row an entry can write, in one entry, decided inside A's section
	// and handed B's record — the record a body would have reached for to name
	// B's rows back when the key came from the value.
	neighbour := h.mustRecord(t, b)
	require.NoError(t, h.withJob(a, func(j *held) error {
		return j.writes().record(neighbour).inProject().open().retained().anchor().commit("writing")
	}))

	require.Equal(t, before, rowsOf(h, b),
		"a section entered on one job wrote a row of another")

	// AND WHAT IT DID WRITE IS THIS JOB. The record's own id is the handle's,
	// so a foreign record lands as this job rather than as that one: the body
	// still decides what the record SAYS, which is what a read-modify-write is,
	// and no longer decides which job it is.
	require.Equal(t, a, h.mustRecord(t, a).ID)
	require.Equal(t, testCommit, h.lastCommit(testRepo, testRef),
		"and the ref anchor is the commit the SECTION READ, not the one the body handed over")
}

func TestAHandleCannotBePointedAtAnotherJob(t *testing.T) {
	fields := reflect.TypeOf(held{})
	var names []string
	for i := range fields.NumField() {
		names = append(names, fields.Field(i).Name)
	}
	require.ElementsMatch(t, []string{"s", "in"}, names,
		"a handle is a service and a holding. A third field is a second answer to something, "+
			"and an `id` among them is a job this package can reassign")

	holding := reflect.TypeOf(section.Holding[jobRecord]{})
	require.Equal(t, "github.com/taubyte/tau/services/build/backend/section", holding.PkgPath())
	for i := range holding.NumField() {
		require.False(t, holding.Field(i).IsExported(),
			"section.Holding.%s is exported, so a handle can be pointed at another job",
			holding.Field(i).Name)
	}

	// AND THE COPY IS ABOUT WHAT THE ORIGINAL WAS ABOUT. A ghost from a section
	// that has closed, handed a live holding, writes the LIVE section's job —
	// it does not resurrect its own.
	h := newHarness(t)
	a := h.push(t, testRef, testCommit)
	b := h.push(t, testRef, shaOf(1))

	ghost := escapee(t, h, b)
	requireRefused(t, h, b, ghost.finish(buildv2.JobStatus_JobStatusCancelled, "cancelled"))

	require.NoError(t, h.withJob(a, func(j *held) error {
		ghost.in = j.in
		require.Equal(t, a, ghost.id(), "the subject came with the holding")
		return ghost.finish(buildv2.JobStatus_JobStatusCancelled, "cancelled")
	}))

	require.Equal(t, buildv2.JobStatus_JobStatusCancelled, h.status(t, a),
		"the write it made is the LIVE section's own, which its own handle could already make")
	require.Equal(t, buildv2.JobStatus_JobStatusOpen, h.status(t, b),
		"and the job the ghost was entered for is untouched")
}

func TestAnAckOfAnotherJobsDeliveryIsRefused(t *testing.T) {
	h := newHarness(t)
	a := h.push(t, testRef, testCommit)
	b := h.push(t, testRef, shaOf(1))

	first, err := h.work.Claim()
	require.NoError(t, err)
	second, err := h.work.Claim()
	require.NoError(t, err)

	neighbour := first
	if neighbour.ID == a {
		neighbour = second
	}
	require.Equal(t, b, neighbour.ID)

	err = h.withJob(a, func(j *held) error { return j.ack(h.work, neighbour) })
	require.Error(t, err)
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
	require.ErrorContains(t, err, "which is another job's")

	_, claimed := h.work.Depth()
	require.Equal(t, 2, claimed, "and the neighbour's item is still there to come back")
}

func TestAnEntryThatCouldNotBeBuiltWritesNothing(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	before := rowsOf(h, id)

	require.NoError(t, h.withJob(unknownJobID, func(j *held) error {
		require.False(t, j.found())
		err := j.writes().record(jobRecord{}).anchor().commit("writing")
		require.Error(t, err)
		require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
		require.ErrorContains(t, err, "a record this section did not find")
		return nil
	}))

	require.Equal(t, before, rowsOf(h, id))
	require.Empty(t, rowsOf(h, unknownJobID),
		"an entry that failed to build must not have written the rows it had already composed")
}

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
		body := func(section.Holding[jobRecord]) error { return nil }
		for b.Loop() {
			if err := l.Do(benchSource{}, body); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("the check a write makes", func(b *testing.B) {
		var l section.Lock[jobRecord]
		write := func() error { return nil }
		if err := l.Do(benchSource{}, func(h section.Holding[jobRecord]) error {
			for b.Loop() {
				if err := h.Write(write); err != nil {
					b.Fatal(err)
				}
			}
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	})
}

// benchSource is the cheapest source there is, so that what the benchmark above
// measures is the section rather than a store.
type benchSource struct{}

func (benchSource) Subject() string                 { return "a job" }
func (benchSource) First() (jobRecord, bool, error) { return jobRecord{}, true, nil }
func (benchSource) Again() (jobRecord, bool, error) { return jobRecord{}, true, nil }

func BenchmarkTheEntry(b *testing.B) {
	h := newHarness(b)
	id := h.push(b, testRef, testCommit)
	rec := h.mustRecord(b, id)

	b.Run("the slice literal an entry replaced", func(b *testing.B) {
		for b.Loop() {
			value, err := rec.encode()
			if err != nil {
				b.Fatal(err)
			}
			ops := []raft.BatchOp{
				{Set: &raft.SetCommand{Key: jobRecordKey(id), Value: value}},
				{Set: &raft.SetCommand{Key: projectIndexKey(rec.ProjectID, id)}},
				{Set: &raft.SetCommand{Key: openIndexKey(rec.RepositoryKey, id)}},
			}
			if len(ops) != 3 {
				b.Fatal("the compiler elided the thing being measured")
			}
		}
	})

	b.Run("the entry that replaced it", func(b *testing.B) {
		if err := h.withJob(id, func(j *held) error {
			for b.Loop() {
				e := j.writes().record(rec).inProject().open()
				if e.err != nil {
					b.Fatal(e.err)
				}
				if e.ops.Len() != 3 {
					b.Fatal("the compiler elided the thing being measured")
				}
			}
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	})
}
