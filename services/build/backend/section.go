package backend

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/raft"
	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
	"github.com/taubyte/tau/pkg/v2/queue"
	"github.com/taubyte/tau/services/build/backend/section"
)

// section.go is where this package's writes are, ALL OF THEM, and there is no
// way to reach one except from inside the transition that decided it.
//
// THE PROPERTY IS OWNERSHIP: "the write this read decided is made while the
// section the read was taken in is still held". It is not "a lock is held", it
// is not "the call sits between two tokens in a file", and it is not "a section
// by this number is running". Seven attempts at that are behind this file, and
// six of them CHECKED it:
//
//   - a table keyed by the READING function could not see a write made by a
//     function that reads nothing (run's two acks);
//   - "the call is after the first jobLock.Lock()" recorded no Unlock at all,
//     so a body that read, released and then wrote was reported as covered;
//   - "the call is inside SOME Lock..Unlock span" cannot see WHICH span;
//   - the pairing scan matched the idents openIndexKey and jobRecordKey, which
//     is a spelling and not a key;
//   - a behavioural needle that TryLocks at the write finds A lock held and not
//     THE lock that decided the write;
//   - a NUMBER read out of the lock is a token a goroutine holding nothing can
//     spend, so it proves RECENCY and never ownership.
//
// THE SEVENTH IS WHY THE SHAPE CHANGED. It made the token unforgeable — a value
// only backend/section could mint — and every write took one. But minting was
// exported and generic over an arbitrary closure, so a caller inside a live
// section could stamp ANY value with the section it held, including one read
// somewhere else entirely. What was proved was that a live section was held at
// the instant the stamp was made. Presence, one indirection further in. Any
// token can be handed to a write that did not earn it, so a check on the token
// is always a check on presence — and the way out is not an eighth check.
//
// SO THERE IS NO TOKEN. A transition is ONE CLOSURE:
//
//	err := s.withJob(id, func(j *held) error {
//	        ...decide on j.rec()...
//	        return j.finish(...)
//	})
//
// withJob takes the lock, takes the barrier, takes the read, and calls the body
// with a handle. Every write in this package is a method on that handle, and
// the handle exists only for the length of the call.
//
// WHAT IS COMPILER-ENFORCED, rather than checked:
//
//	there is no token to forge      nothing hands one out: no Enter, no
//	                                accessor, no "which section is running"
//	there is no read to re-stamp    withJob takes the read itself, before the
//	                                body runs, and no minting function exists.
//	                                Nor can a stale read be given a live
//	                                holding: the record IS a field of the
//	                                holding, so `stale.in = live.in` copies the
//	                                live record along with it, and the fields
//	                                that would separate the two are unexported
//	                                in another package
//	a write cannot be made outside  every write is a method on *held, and the
//	                                only *held is withJob's argument
//	a helper carries the handle      ...or it cannot write at all, which is the
//	                                same property stated by a parameter list
//	                                rather than by a scan
//
// WHAT IS THE RUNTIME'S, and cannot be the compiler's. Go has no lifetime on a
// parameter: a body can capture its handle in a closure that outlives the call,
// defer a method on it, or send it to another goroutine. THE HANDLE IS DEAD THE
// MOMENT withJob RETURNS, so all three are refused rather than made — an error
// and a delivery that comes back, instead of a write decided on a read
// something else was free to invalidate. section_test.go drives each of them.
//
// WHAT IS STILL A GUARD, in population_test.go, because no type can say it:
// which functions may reach Store.Batch and the queue's three paired writes at
// all — a body that ignores the handle and calls s.store.Batch itself is a
// write nothing here can prevent, only enumerate — plus which KEY an entry may
// write, and that the read is taken in one place. Those fail at review time and
// cover the half this cannot.
//
// WHAT IT COSTS. One mutex acquisition and two atomic stores per section, one
// atomic store on the way out, and one atomic load plus a nil check per write —
// alongside a replicated raft entry. section_test.go's BenchmarkSection
// measures it rather than asserting it is negligible. It is compiled in
// everywhere: a check that is only in the test build is a production binary
// running code no test ran.

// jobLock makes a job record's read and the entry that read decides ONE
// decision, wherever in this package the two appear.
//
// EVERY TRANSITION OF A JOB IS READ-MODIFY-WRITE. Enqueue's idempotency, the
// claim, the settle, a cancel, a retry, the dead-letter drain and reconcile all
// read the record, decide on what it said, and write it back — and a read is
// only true until something else writes.
//
// NOTHING OFF THIS PROCESS CAN WRITE ONE. A write commits on the leader alone,
// so another node deciding this same job has to take leadership first and this
// node's Batch then does not commit — and withJob takes a Barrier before the
// read its writes are decided on, so a node that has just won an election
// cannot decide on entries it has not applied. THE BARRIER IS THE ENTRY
// POINT'S, once, rather than a line each of nine transitions had to remember:
// withJobUnbarriered is the one exception and it is a different name with one
// caller, mend, which triages on a read that decides nothing and pays for the
// barrier on the branch that deletes a row. reconcile.go says why, and
// barrier_test.go measures what both placements cost.
//
// AND NOTHING IN THIS PROCESS STOPPED IT EITHER. Cancel and Retry answer on rpc
// goroutines while the claim loop runs on its own, and raft ORDERS their two
// entries without noticing that the second was decided on a read taken before
// the first — and the store has no compare-and-swap for them to arbitrate with.
// So they take a lock, which is the same three-part argument pkg/v2/queue makes
// for its own claim, over the same store, for the same reason.
//
// WHAT IT DOES NOT COVER, said out loud so it is not mistaken for more. It is
// one process's third of an exclusion whose other two thirds are leadership and
// the barrier, so it is the mechanism only while all three hold:
//
//   - every write of a record goes through Store.Batch, which refuses off the
//     leader. A writer that went around it — a cache, a kvdb, a direct Set —
//     would be outside this entirely, which is why Store names no Set.
//   - nothing claims, settles or reconciles off the leader. step() refuses on a
//     follower and so does every queue write under it. A backend that ran work
//     somewhere the leader is not would need the STORE to arbitrate.
//   - the store keeps one writer. Two nodes that both believe they lead cannot
//     both commit, because the stale one's entry never reaches quorum, so this
//     lock is never asked to arbitrate across processes. A store without that
//     property needs a CONDITIONAL WRITE instead, and raft.BatchOp has exactly
//     two operations, Set and Delete — there is nothing to condition on.
//   - the queue's own claim and sweep are outside it. Both are pkg/v2/queue's
//     transitions rather than this table's: they are decided under the queue's
//     group lock, on no job record. The record write a claim pairs with is
//     begin's, and that is a section.
//
// It is the package's rather than a field on Service because what it protects
// is a job table, not a Service: nothing stops two Services from being
// constructed over one store, and runner_test.go's follower is exactly that.
// Two locks there would be two unsynchronised writers of one table.
//
// LOCK ORDER IS jobLock, THEN pkg/v2/queue's GROUP LOCK, never the reverse. A
// transition holds this across the queue write that pairs with its record write
// — a push, a withdrawal, an ack — and pkg/v2/queue calls nothing in this
// package, so the order cannot invert. IT IS ALSO NOT REENTRANT: a body that
// reached for withJob again would deadlock against itself, which is why
// population_test.go enumerates that nothing under a body does.
//
// THAT ORDER IS ALSO WHY THE OUTCOME OF A LOST PAIRING IS HARD TO REPRODUCE.
// pkg/v2/queue takes its group lock before it touches the store, so an ack
// already holds it by the time anything a test can hook has run, and a racing
// push queues up behind rather than landing in front. The harm needs the racer
// to win that lock; the fact does not — which is why the mechanism is a handle
// whose writes are refused rather than an interleaving somebody has to catch.
//
// THE PAIRING IS THE RULE AND NOT AN ILLUSTRATION, and the sentence above was
// once true of every transition except the one that ran the builds. run acked
// its deliveries with no lock held, at both of its exits, and the decision each
// ack carried out had been taken in a section that had already closed. That is
// not a lost update between two record writes; it is a queue write and a record
// write drifting apart. queue.Push is a no-op for an id the queue already
// holds, and queue.Ack deletes the very index row that dedup reads — so a
// retry that landed while a build was running made a push that did nothing, and
// the ack after the build destroyed the item it had been deduplicated against.
// The caller had been told its retry was accepted; the job had no queue item at
// all, and once settle had dropped its open row, nothing for reconcile to walk
// either. Both acks are now methods on the handle the section handed the body.
//
// WHAT IS DELIBERATELY OUTSIDE, so that the rule is not read as "everything":
// the build, and the round trip to auth. A section that held either would hold
// this lock for an hour, or across a network call to another service — and the
// whole point of it is that it is only ever held across writes to one store.
var jobLock section.Lock[jobRecord]

// held is ONE JOB UNDER THE LOCK: the record read for it, and every write that
// read can decide. It is the only route in this package to Store.Batch, to the
// queue's push, ack and withdrawal, and to the map that says what this node is
// running.
//
// IT EXISTS ONLY INSIDE A BODY. withJob makes one, hands it over, and the
// holding it carries is dead the moment withJob returns — so a write in a
// helper takes one of these or cannot write, and a write from a handle that
// outlived its call is refused. There is nothing here to forge: assembling a
// held is assembling one around a holding, and the only live holding is the one
// a running body was given.
type held struct {
	s  *Service
	id string
	in section.Holding[jobRecord]
}

// withJob is EVERY TRANSITION'S SHAPE: the lock, the barrier, the read, and a
// handle that is the only way to write and lives only as long as the call.
//
// The barrier is here rather than in each body for the same reason the read is:
// a rule every caller has to remember is a rule the tenth caller forgets, and
// "this section took no barrier" is invisible in a diff. A node that has just
// become leader holds committed entries it has not applied, so a read without
// one can be a Cancel this transition cannot see.
func (s *Service) withJob(id string, body func(*held) error) error {
	return s.enter(id, true, body)
}

// withJobUnbarriered is mend's alone, and its name is the exception.
//
// A barrier is a replicated log entry. reconcile walks every open job in the
// cloud, so taking one before each row's read would cost one entry per open job
// per pass forever, where the pass is triage: most rows are live and decide
// nothing. mend therefore reads without one, and takes the barrier — with
// held.reread — on the branch that deletes a row, which in steady state is no
// rows at all. reconcile.go argues it and barrier_test.go measures both.
func (s *Service) withJobUnbarriered(id string, body func(*held) error) error {
	return s.enter(id, false, body)
}

// enter is the ONE PLACE A HANDLE IS MADE, which is what makes "the handle is
// the section" true rather than nearly true.
func (s *Service) enter(id string, barrier bool, body func(*held) error) error {
	return jobLock.Do(func() (jobRecord, bool, error) {
		if barrier {
			if err := s.lead(); err != nil {
				return jobRecord{}, false, err
			}
		}
		return s.readRecord(id)
	}, func(in section.Holding[jobRecord]) error {
		return body(&held{s: s, id: id, in: in})
	})
}

// rec is the record this section read. found is whether there was one.
func (h *held) rec() jobRecord { return h.in.Value() }
func (h *held) found() bool    { return h.in.Found() }

// reread takes the barrier and reads the record AGAIN, under this same holding
// — mend's second read, and the only one in this package.
//
// It is not a way to re-stamp anything: the value it installs comes from a read
// this call makes, inside the holding this call is running in. A body that
// wanted a value from anywhere else would have nothing to put it in.
func (h *held) reread() error {
	return h.in.Reread(func() (jobRecord, bool, error) {
		if err := h.s.lead(); err != nil {
			return jobRecord{}, false, err
		}
		return h.s.readRecord(h.id)
	})
}

// under is the one gate, and every write below goes through it. THE WRITE IS
// THE ARGUMENT rather than something done after asking: a gate that answers a
// question leaves a caller free to ask it and write anyway, on the branch that
// forgot or in the wrong order.
//
// A REFUSAL IS THIS PROCESS'S OWN FAULT, so it is CodeInternal. See refused().
func (h *held) under(w func() error) error {
	if err := h.in.Write(w); err != nil {
		if errors.Is(err, section.ErrStale) {
			return stale(err)
		}
		return err
	}
	return nil
}

// refused spells a write that did not happen, keeping why it did not in the
// wire code and not only in the message.
//
// A STORE THAT REFUSED IS Unavailable — "ask somebody else" — and that is the
// one code services/build/clients' Pool.Do walks past to the next peer
// (leader.go). A SECTION THAT HAD ALREADY RETURNED IS NOT THAT. It is this
// process's own fault, identical on every node in the cloud, and answering it
// Unavailable would send a caller round every peer it knows looking for one
// where the code is different.
// IT WRAPS RATHER THAN FORMATS, so that errors.Is still reaches ErrStale at the
// top of the call: a test that asserts a write was refused for the reason it
// was refused asks the sentinel, not the message.
func refused(err error, format string, args ...any) error {
	if errors.Is(err, section.ErrStale) {
		return errInternal(format+": %w", append(args, err)...)
	}
	return errUnavailable(format+": %w", append(args, err)...)
}

// stale turns a refusal into this package's wire code. CodeInternal, because
// there is no other node to ask.
func stale(err error) error { return errInternal("%w", err) }

// commit writes ONE ENTRY on behalf of the read that decided it, and is this
// package's only path to Store.Batch — attemptSink.write excepted, and named as
// the exception in population_test.go, because a log or asset row is content
// keyed by attempt that no record read decides.
func (h *held) commit(ops []raft.BatchOp) error {
	return h.under(func() error { return h.s.store.Batch(ops, h.s.cfg.Queue.ApplyTimeout) })
}

// push gives this job an item, on behalf of the read that decided it should
// have one.
func (h *held) push() error {
	return h.under(func() error { return h.s.work.Push(h.id, nil) })
}

// ack removes an item for good, on behalf of the read that decided it should
// go: begin's read of a job that has already finished, the dead-letter drain's
// of one it has just failed, and settle's of the record this run's outcome is
// about.
//
// AN ACK IS THE ONE QUEUE WRITE WITH NOTHING BEHIND IT. It deletes the very
// index row a concurrent push deduplicates against, so an ack carried out on a
// decision taken in a section that has closed destroys an item its caller was
// told it had. There is no way left to spell that: the ack is a method on the
// handle the deciding read came with.
//
// settle acked BEFORE its read until the round before last, because queue.Ack
// barriers and its section had no other one. That ordering was real and it is
// now paid for rather than worked around: settle takes withJob's barrier like
// every other section, and its ack is an ordinary write inside the body. It
// costs one replicated entry per settled build — barrier_test.go spends it —
// and it is what makes "every write is the body's" true with no exception.
func (h *held) ack(q *queue.Queue, d queue.Delivery) error {
	return h.under(func() error {
		if err := q.Ack(d); err != nil {
			return fmt.Errorf("ack %s: %w", d.ID, err)
		}
		return nil
	})
}

// withdraw takes a cancelled job's item out of the queue.
//
// LEAVING IT WAS NOT FREE. finish() drops the open index row, so the repository
// is told it has nothing outstanding while a delivery for it is still queued —
// and that item counts against MaxQueueDepth, and costs the builder a whole
// claim cycle before it reaches a job somebody still wants. On a leader that
// runs no builds — a voter rather than a builder, which WithRunner says is a
// deployment's choice — nothing ever claims, so nothing ever drops it.
//
// The two refusals are the cases where there is nothing to withdraw. An item
// the queue does not hold is one already acked, or one whose push never landed.
// An item a CONSUMER holds is refused by the queue on purpose: that claim is
// the mutual exclusion, and taking it away would let a retry of this same job
// be claimed beside the run still going.
//
// SWALLOWING ErrClaimed IS ONLY HONEST IF THAT ITEM ALWAYS LEAVES, and it does,
// by one of three exits — none of which needs anybody to come back to it:
//
//   - the holder acks. On this node the stopRun() beside this call is what
//     makes that prompt, and it reaches the run because begin registered it inside
//     the same section this one is running in. On another node the run
//     finishes, acks, and settles against a record that is already terminal.
//   - the holder dies, the claim lapses at its deadline, and the redelivery is
//     dropped on reading the record: begin refuses a job it finds terminal and
//     run acks it without starting it.
//   - the redeliveries run out and the item reaches the dead letter, where the
//     drain finds a terminal record, writes nothing, and acks.
//
// Before the section existed the first exit was the claim: the stop could be
// called while the run had been decided on and not yet registered, so it was a
// no-op and the build ran to its own completion. It is the registration inside
// begin, not this call, that closed that.
func (h *held) withdraw() error {
	return h.under(func() error {
		err := h.s.work.Remove(h.id)
		if err == nil || errors.Is(err, queue.ErrNotFound) || errors.Is(err, queue.ErrClaimed) {
			return nil
		}
		return errUnavailable("withdrawing job %s from the queue: %v", h.id, err)
	})
}

// register records the run the claim's read decided, so that a Cancel
// serialized after that section finds something to stop.
//
// A MAP IN THIS PROCESS IS A WRITE LIKE ANY OTHER, and it goes through the same
// door as the three above for the same reason. Two arguments in this package
// rest their whole weight on this write being made inside the section that
// claimed the job: runner.go's begin (a cancel serialized after the claim must
// find the run registered, or it writes a terminal record while a build it
// could have stopped runs on to its own deadline) and withdraw above.
//
// Its only guard was a scan that asked whether the assignment sat inside SOME
// jobLock span, which cannot see which span — and a registration handed to
// run() as a closure built inside the section and invoked after begin returns
// is inside the span by every measure a scan has, and outside the section by
// the only one that matters. As a method on the handle it is refused.
//
// forget is its counterpart and is not a method here: it is not decided by a
// read but by the run being over, it runs outside every section (the build
// holds no lock), and deleting the cancel of a run that has ended is what run's
// defer is for. The claim loop is the only goroutine that claims
// (TestTheClaimLoopIsTheOnlyGoroutineThisPackageStarts), so the entry it drops
// is always the one this same goroutine registered.
func (h *held) register(stop context.CancelFunc) error {
	return h.under(func() error {
		h.s.runningMu.Lock()
		defer h.s.runningMu.Unlock()
		h.s.running[h.id] = stop
		return nil
	})
}

// stopRun ends a run THIS NODE is holding. It is a no-op for a job running
// somewhere else or not running at all, which is the common case and not an
// error: the record write is what cancels those.
//
// IT IS A WRITE AND IT WAS THE LAST ONE WITH NO DOOR. It reads the running map
// and INVOKES a context.CancelFunc, which closes a channel a build is selecting
// on — decided by cancel's read of the record ("this job is not terminal, so
// stop its run") and carrying it out. It reached no store and took no reading,
// so every enumeration in population_test.go walked past it: the map's WRITERS
// were listed, and this only reads it.
//
// WHAT THAT COST, measured rather than argued: a stop carried out after its
// section had closed kills a build a LATER retry registered, and that job ends
// Failed("context canceled") having never run. Inside the section the two
// orders are the only two — this one first, and the claim's re-read finds a
// cancelled record and never starts; the claim first, and the registration is
// already here — and section_test.go drives the escaped one.
func (h *held) stopRun() error {
	return h.under(func() error {
		h.s.runningMu.Lock()
		end, ok := h.s.running[h.id]
		h.s.runningMu.Unlock()
		if ok {
			end()
		}
		return nil
	})
}

// finish writes a terminal record in one Batch: the record, the open index row
// removed, the retention index row written, plus whatever else belongs in the
// same entry — on success that is the ref anchor, so "this job succeeded" and
// "this is now the commit to diff from" cannot end up disagreeing. A job is
// never in a state where it is finished but still listed as open, or listed as
// open but has no record.
func (h *held) finish(status buildv1.JobStatus, reason string, extra ...raft.BatchOp) error {
	now := h.s.now()
	rec := h.rec()
	rec.Status = int32(status)
	rec.UpdatedAt = now.Unix()
	rec.ClaimedUntil = 0
	rec.Holder = ""
	rec.Reason = reason

	value, err := rec.encode()
	if err != nil {
		return errInternal("encoding job %s: %v", h.id, err)
	}

	ops := append([]raft.BatchOp{
		set(jobRecordKey(h.id), value),
		del(openIndexKey(rec.RepositoryKey, h.id)),
		set(terminalIndexKey(now, h.id), nil),
	}, extra...)
	if err := h.commit(ops); err != nil {
		return refused(err, "finishing job %s", h.id)
	}
	return nil
}

// requeue gives an open job an item to be claimed by, and is a no-op for one
// that is claimed or finished — a job somebody is running does not want a
// second item, and a finished one does not want to be run again.
//
// RECORD FIRST, PUSH SECOND wherever the two appear together, and they cannot
// be fused because queue.Push does its own Batch. A crash between them leaves a
// record with no item, which reconcile repairs and which a redelivered webhook
// already answers correctly. The other order leaves an item with no record — an
// unidentifiable job nobody can run — which is exactly v1's orphan.
func (h *held) requeue() error {
	rec := h.rec()
	if rec.terminal() || rec.status() == buildv1.JobStatus_JobStatusClaimed {
		return nil
	}
	if err := h.push(); err != nil {
		return refused(err, "queue push")
	}
	return nil
}
