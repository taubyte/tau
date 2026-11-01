package backend

import (
	"context"
	"errors"
	"fmt"
	"time"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/queue"
	"github.com/taubyte/tau/services/build/backend/row"
	"github.com/taubyte/tau/services/build/backend/section"
)

// section.go holds every write in this package, reachable only from inside the
// transition that decided it, about the job that transition read:
//
//	WHEN       made while the section the read was taken in is still held
//	WHO        made by the body that took that read, or what it handed its handle
//	ABOUT WHAT every row it names belongs to the job the read was taken on
//
// A transition is one closure:
//
//	err := s.withJob(id, func(j *held) error {
//	        ...decide on j.rec()...
//	        return j.finish(...)
//	})
//
// withJob takes the lock, the barrier and the read, and hands the body a handle.
// Every write is a method on it, and every key is composed from h.in.Subject(), a
// field of backend/section this package cannot assign. Rows are a row.Entry, made
// only by Set and Delete and reaching the store only by Apply. Go has no lifetime
// on a parameter, so a handle that outlives withJob (captured, deferred, sent) is
// refused at runtime (section_test.go). What no type can say (who may reach a
// batch or the queue's paired writes, which key each row builds) is enumerated in
// population_test.go. The cost is one mutex and a few atomics per section,
// measured by BenchmarkSection.

// jobLock makes a job record's read and the entry it decides one decision. Every
// transition is read-modify-write, and the store has no compare-and-swap. Writes
// commit on the leader only, and withJob barriers before the read, so no other
// process can interleave; this lock covers Cancel and Retry on rpc goroutines
// against the claim loop. One lock over the whole table, with per-job sections:
// the subject, not the lock, scopes the rows. It holds only while writes go
// through Store.Batch, work runs only on the leader, and the store keeps one
// writer. Package-level, since two Services may share one store.
//
// Lock order is jobLock, then pkg/queue's group lock; it is not reentrant. The
// queue write paired with a record write (push, withdrawal, ack) is inside the
// section, since Ack deletes the index row a concurrent Push dedups against.
// Outside it: the build, the auth round trip, and the log handoff to the asset
// store.
var jobLock section.Lock[jobRecord]

// jobReader is which job a section is about and where its record comes from,
// chosen once by enter(). First barriers when withJob asked for one; Again always
// does, for mend's delete branch.
type jobReader struct {
	s       *Service
	id      string
	barrier bool
}

// Subject is the job this section is about. Every key every write in this file
// composes is built from it.
func (r jobReader) Subject() string { return r.id }

func (r jobReader) First() (jobRecord, bool, error) {
	if r.barrier {
		if err := r.s.lead(); err != nil {
			return jobRecord{}, false, err
		}
	}
	return r.s.readRecord(r.id)
}

func (r jobReader) Again() (jobRecord, bool, error) {
	if err := r.s.lead(); err != nil {
		return jobRecord{}, false, err
	}
	return r.s.readRecord(r.id)
}

// held is one job under the lock: its record and every write the read can decide,
// the only route to Store.Batch, the queue's push, ack and withdrawal, and the
// running map. It exists only inside a body and is dead once withJob returns. It
// has no id field: the id is inside the holding, readable but not assignable here.
type held struct {
	s  *Service
	in section.Holding[jobRecord]
}

// withJob is every transition's shape: lock, barrier, read, and a handle that is
// the only way to write. The barrier is here so no transition forgets it.
func (s *Service) withJob(id string, body func(*held) error) error {
	return s.enter(id, true, body)
}

// withJobUnbarriered is mend's alone: a barrier per row of a reconcile pass would
// be one log entry per open job, so mend barriers only on its delete branch.
func (s *Service) withJobUnbarriered(id string, body func(*held) error) error {
	return s.enter(id, false, body)
}

// enter is the one place a handle is made and a subject named.
func (s *Service) enter(id string, barrier bool, body func(*held) error) error {
	return jobLock.Do(
		jobReader{s: s, id: id, barrier: barrier},
		func(in section.Holding[jobRecord]) error { return body(&held{s: s, in: in}) },
	)
}

// id reads the subject out of the holding, so no assignment here can re-point a
// handle.
func (h *held) id() string { return h.in.Subject() }

// rec is the record this section read. found is whether there was one.
func (h *held) rec() jobRecord { return h.in.Value() }
func (h *held) found() bool    { return h.in.Found() }

// reread takes the barrier and reads this job's record again under the same
// holding; it takes nothing, so no foreign value can be installed.
func (h *held) reread() error { return h.in.Reread() }

// under is the one gate, taking the write as its argument so no caller can ask
// and then write anyway. A refusal is CodeInternal (refused).
func (h *held) under(w func() error) error {
	if err := h.in.Write(w); err != nil {
		if errors.Is(err, section.ErrStale) {
			return stale(err)
		}
		return err
	}
	return nil
}

// refused spells a write that did not happen. A store refusal is Unavailable (ask
// another peer); a section that had already returned is this process's own fault.
// It wraps so errors.Is still reaches ErrStale.
func refused(err error, format string, args ...any) error {
	if errors.Is(err, section.ErrStale) {
		return errInternal(format+": %w", append(args, err)...)
	}
	return errUnavailable(format+": %w", append(args, err)...)
}

// stale turns a refusal into this package's wire code. CodeInternal, because
// there is no other node to ask.
func stale(err error) error { return errInternal("%w", err) }

// entry is one replicated entry under construction. Every row is composed here
// from the handle's subject (the one exception, the anchor, from the record the
// section read). Nothing takes a key or hands rows in or out, and the rows are a
// row.Entry.
type entry struct {
	h   *held
	ops row.Entry
	// err carries a record encoding failure to commit(), so a chain reads as one
	// expression; nothing is written before commit.
	err error
}

// stated is an entry carrying this job's record. Rows that are claims about the
// record (open, project, retention, anchor) exist only on stated, so an entry
// without a record cannot write them. dropRetained and dropOrphanedOpen stay on
// entry.
type stated struct {
	*entry
	rec jobRecord
}

// writes opens an entry about this job. It is the only constructor there is.
func (h *held) writes() *entry {
	// Four is the widest fixed entry: terminal record, open row, retention row, ref
	// anchor. Artifact rows grow it.
	return &entry{h: h, ops: row.Sized(4)}
}

// No put(key) or drop(key): a method taking a key would let any handle write any
// key. Each row method composes its own key.

// record writes this job's record; rec.ID is overwritten with the subject, so
// foreign contents still land on this job.
func (e *entry) record(rec jobRecord) stated {
	rec.ID = e.h.id()
	value, err := rec.encode()
	if err != nil {
		e.err = errInternal("encoding job %s: %v", e.h.id(), err)
		return stated{entry: e, rec: rec}
	}
	e.ops.Set(jobRecordKey(e.h.id()), value)
	return stated{entry: e, rec: rec}
}

// open lists this job as outstanding for its repository, and dropOpen takes it
// off that listing. The repository is the record's, and the id is the handle's.
func (s stated) open() stated {
	s.ops.Set(openIndexKey(s.rec.RepositoryKey, s.h.id()), nil)
	return s
}

func (s stated) dropOpen() stated {
	s.ops.Delete(openIndexKey(s.rec.RepositoryKey, s.h.id()))
	return s
}

// inProject is how a caller FINDS this job. It is written once, by create, in
// the same entry as the record it indexes.
func (s stated) inProject() stated {
	s.ops.Set(projectIndexKey(s.rec.ProjectID, s.h.id()), nil)
	return s
}

// retained is the retention row, at the instant this entry's record says the job
// finished.
func (s stated) retained() stated {
	s.ops.Set(terminalIndexKey(time.Unix(s.rec.UpdatedAt, 0), s.h.id()), nil)
	return s
}

// dropRetained removes the retention row a previous finish wrote, keyed off the
// record this section read rather than the one being written.
func (e *entry) dropRetained() *entry {
	e.ops.Delete(terminalIndexKey(time.Unix(e.h.rec().UpdatedAt, 0), e.h.id()))
	return e
}

// artifacts points this project's deployments at what this job built, in the same
// entry as the record, so success and what a deployment runs cannot disagree.
// Keys come from the job's own project and the run's seg-escaped asset names.
// Only a successful build moves it.
func (s stated) artifacts(p produced) stated {
	if !s.h.found() {
		s.err = errInternal("job %s: artifacts decided on a record this section did not find", s.h.id())
		return s
	}

	if s.h.rec().ProjectID == "" {
		// A job with no project is one nothing can address an artifact under.
		// It is not an error — there is simply nowhere to file what it built.
		return s
	}

	for ref, cid := range p.assets {
		s.ops.Set(artifactKey(s.h.rec().ProjectID, ref.environment, ref.resource), []byte(cid))
	}
	return s
}

// anchor is the "before" of the next build of this ref: the one row a job owns not
// keyed by its id, keyed by (repository, ref) because the next build of that ref is
// another job. All parts come off the record this section read; no record, no
// anchor.
func (s stated) anchor() stated {
	if !s.h.found() {
		s.err = errInternal("job %s: a ref anchor decided on a record this section did not find", s.h.id())
		return s
	}
	s.ops.Set(refLastKey(s.h.rec().RepositoryKey, s.h.rec().Ref), []byte(s.h.rec().CommitSHA))
	return s
}

// dropOrphanedOpen is the named exception: when the record is gone the repository
// comes in from the listed row, while the id is still the handle's. Its one caller
// is enumerated in population_test.go.
func (e *entry) dropOrphanedOpen(repositoryKey string) *entry {
	e.ops.Delete(openIndexKey(repositoryKey, e.h.id()))
	return e
}

// commit applies the entry, the only path to a replicated batch. It takes no rows;
// `what` is the gerund for the failure message.
func (e *entry) commit(what string) error {
	if e.err != nil {
		return e.err
	}
	if err := e.h.under(func() error {
		return e.ops.Apply(e.h.s.store, e.h.s.cfg.Queue.ApplyTimeout)
	}); err != nil {
		return refused(err, "%s job %s", what, e.h.id())
	}
	return nil
}

// push gives this job an item, on behalf of the read that decided it should
// have one.
func (h *held) push() error {
	return h.under(func() error { return h.s.work.Push(h.id(), nil) })
}

// ack removes an item for good, decided by begin's, the drain's or settle's read.
// An ack outside its deciding section would destroy an item a concurrent push
// deduplicated against. The delivery is checked against the subject.
func (h *held) ack(q *queue.Queue, d queue.Delivery) error {
	return h.under(func() error {
		if d.ID != h.id() {
			return errInternal("job %s: acking delivery %s, which is another job's", h.id(), d.ID)
		}
		if err := q.Ack(d); err != nil {
			return fmt.Errorf("ack %s: %w", d.ID, err)
		}
		return nil
	})
}

// withdraw takes a cancelled job's item out of the queue, so it neither counts
// against MaxQueueDepth nor lingers on a leader that runs no builds. An item a
// consumer holds is refused by the queue; it still always leaves: the holder
// acks (stopRun makes that prompt locally), its claim lapses and the redelivery is
// dropped, or it reaches the dead letter and is acked.
func (h *held) withdraw() error {
	return h.under(func() error {
		err := h.s.work.Remove(h.id())
		if err == nil || errors.Is(err, queue.ErrNotFound) || errors.Is(err, queue.ErrClaimed) {
			return nil
		}
		return errUnavailable("withdrawing job %s from the queue: %v", h.id(), err)
	})
}

// register records the run the claim decided, inside that section, so a Cancel
// serialized after it finds something to stop. Keyed by the subject. forget, its
// counterpart, runs outside every section when the run ends.
func (h *held) register(stop context.CancelFunc) error {
	return h.under(func() error {
		h.s.runningMu.Lock()
		defer h.s.runningMu.Unlock()
		h.s.running[h.id()] = stop
		return nil
	})
}

// stopRun ends a run this node holds, a no-op otherwise. A write like any other:
// carried out after its section, it could kill a build a later retry registered.
func (h *held) stopRun() error {
	return h.under(func() error {
		h.s.runningMu.Lock()
		end, ok := h.s.running[h.id()]
		h.s.runningMu.Unlock()
		if ok {
			end()
		}
		return nil
	})
}

// ends builds the entry finishing this job: the record (carrying what the run
// produced), the open row removed, the retention row written. It returns the entry
// so settle can add the ref anchor to it.
func (h *held) ends(status buildv2.JobStatus, reason string, p produced) stated {
	rec := h.rec().with(p)
	rec.Status = int32(status)
	rec.UpdatedAt = h.s.now().Unix()
	rec.ClaimedUntil = 0
	rec.Holder = ""
	rec.Reason = reason
	return h.writes().record(rec).dropOpen().retained()
}

// finish ends this job and writes nothing else. It is the terminal write of the
// transitions that ran NO BUILD — a cancel, a dead letter drain — so there is
// nothing produced to carry.
func (h *held) finish(status buildv2.JobStatus, reason string) error {
	return h.ends(status, reason, produced{}).commit("finishing")
}

// fail ends this job as Failed and records what the failed run produced.
func (h *held) fail(reason string, p produced) error {
	return h.ends(buildv2.JobStatus_JobStatusFailed, reason, p).commit("finishing")
}

// succeed ends this job, records what it produced and moves the ref anchor in one
// entry.
func (h *held) succeed(p produced) error {
	return h.ends(buildv2.JobStatus_JobStatusSuccess, "", p).artifacts(p).anchor().commit("finishing")
}

// logged records what a run produced without moving the status: for a cancelled
// job (its status is not this run's to move) and a reopened one. The log is filed
// under the run's attempt, so it never lands on another run's line. Nothing
// produced, nothing written.
func (h *held) logged(p produced) error {
	if p.logCID == "" && len(p.assets) == 0 {
		return nil
	}
	return h.writes().record(h.rec().with(p)).commit("recording the output of")
}

// requeue gives an open job an item; claimed or finished jobs get none. Record
// first, push second (Push does its own Batch): a crash between leaves a record
// with no item, which reconcile repairs, never an unidentifiable item.
func (h *held) requeue() error {
	rec := h.rec()
	if rec.terminal() || rec.status() == buildv2.JobStatus_JobStatusClaimed {
		return nil
	}
	if err := h.push(); err != nil {
		return refused(err, "queue push")
	}
	return nil
}
