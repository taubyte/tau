package backend

import (
	"context"
	"errors"
	"fmt"
	"time"

	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
	"github.com/taubyte/tau/pkg/v2/queue"
	"github.com/taubyte/tau/services/build/backend/row"
	"github.com/taubyte/tau/services/build/backend/section"
)

// section.go is where this package's writes are, ALL OF THEM, and there is no
// way to reach one except from inside the transition that decided it, ABOUT THE
// JOB THAT TRANSITION READ.
//
// THE PROPERTY HAS THREE CLAUSES, and it was stated with two for eight rounds:
//
//	WHEN      the write is made while the section the read was taken in is
//	          still held;
//	WHO       it is made by the body that took that read, or by something that
//	          body handed its handle to;
//	ABOUT WHAT every row the write names is a row of the job that read was
//	          taken on.
//
// The first two are the seven rounds behind this file. Six of them CHECKED the
// first:
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
// the instant the stamp was made. Presence, one indirection further in.
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
// # And the eighth round is ABOUT WHAT
//
// THE LADDER ANSWERED "WHEN" AND "WHO" ALL THE WAY UP AND NEVER "ABOUT WHAT".
// commit([]raft.BatchOp) took arbitrary entries and finish(..., extra ...) took
// arbitrary extras, so a body inside a perfectly live section, holding a handle
// nobody could forge, could put a row of ANOTHER JOB in its entry — one line
// inside a literal, in a method already on every writer list — and the whole
// suite stayed green. Nothing compared the id inside a key with the id the read
// was taken on, because no id inside a key ever came from the handle.
//
// It is not hypothetical either: begin's entry was a row at
// jobRecordKey(rec.ID) inside withJob(d.ID, …), where the two agreeing was a
// fact about the record on disk rather than about the expression.
//
// SO A HANDLE'S WRITES COMPOSE THEIR OWN KEYS. There is no []raft.BatchOp in
// this file's parameter lists any more. A body asks the handle for an entry and
// names the ROWS it wants — the record, the open row, the project row, the
// retention row, the ref anchor — and each of them is built from
// h.in.Subject(), which is the id the section's read was taken on and which
// this package cannot assign, because it is a field of another package's
// struct.
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
//	there is no value to install    Reread takes no closure. It re-reads the
//	                                SUBJECT, through the source named at the
//	                                door, and a body has nowhere to put a value
//	                                it read anywhere else
//	there is no id to re-point      a handle has no id field. `other := *j;
//	                                other.id = neighbour` does not compile,
//	                                because the id lives inside the holding
//	a write cannot be made outside  every write is a method on *held, and the
//	                                only *held is withJob's argument
//	a write cannot name another job every key an entry writes is composed from
//	                                the handle's own subject; a row of another
//	                                job has no expression
//	a row cannot be composed at all an entry's rows are a row.Entry, whose
//	                                slice is unexported in another package. A
//	                                row is MADE INTO one — e.ops.Set(…) — so
//	                                `append(e.ops, raft.BatchOp{…})` does not
//	                                compile, and neither does anything else
//	                                that treats the rows as a value
//	nor handed to the store beside  Apply takes the STORE, not the rows. There
//	the ones it composed            is no expression of raft's batch type in
//	                                this package, so `Batch(Ops(append(e.ops,
//	                                forged)), t)` — the respelling that walked
//	                                past a guard reading the outermost callee's
//	                                name — has nothing left to name
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
// which functions may reach a replicated batch and the queue's three paired
// writes at all — a body that ignores the handle and calls s.store.Batch itself
// is a write nothing here can prevent, only enumerate — WHICH KEY each row
// builds, and that the read is taken in one place. Those fail at review time and
// cover the half this cannot. "Which expressions may build a raft.BatchOp" was
// on that list until the ninth round and "what is handed to the store" until the
// tenth; both are the compiler's now, because neither a row nor a batch of them
// has an expression outside backend/row.
//
// AND WHAT IS LEFT ON THAT LIST HOLDS A NAME. "These functions may name Set,
// Delete and Apply" is a check on a SPELLING: the act it stands for is composing
// a row or reaching a store, and a scan reads the identifier at the call. It is
// written down here and beside each check rather than left as a claim about
// acts, because three rounds running the thing that survived was a check on an
// argument and the thing that beat it was a respelling.
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
// IT IS ONE LOCK OVER THE WHOLE TABLE AND THE SECTIONS ARE PER JOB. The lock is
// the exclusion; the SUBJECT is what a section is about, and it is named at the
// door, carried in the holding, and composed into every key a write builds.
// Two transitions of two different jobs still serialize — a table-wide mutex is
// cheap next to a replicated entry, and a lock per job is a map of locks that
// has to be reaped — but neither of them can write the other's rows.
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
// the build, the round trip to auth, AND THE HANDOFF OF THE LOG TO THE ASSET
// STORE. A section that held any of them would hold this lock for an hour, or
// across a network call to another service — and the whole point of it is that
// it is only ever held across writes to one store.
//
// THE THIRD OF THOSE WAS INSIDE FOR A ROUND, which is why it is now named here
// rather than left to the reader. The publish had been an argument to settle,
// evaluated before the ack; the round that fixed that moved it into settle's
// body, and a body is this lock. One slow storage node then stalled webhook
// ingestion and every user-facing cancel and retry on the node for up to
// PublishTimeout per settled build — measured: an unrelated repository's
// Enqueue still waiting after 300ms, and away 0.58ms after the store was
// released. settle is two sections with the publish between them now, and
// output_test.go keeps the measurement.
var jobLock section.Lock[jobRecord]

// jobReader is WHICH JOB a section is about and where its record comes from,
// named at the door and nowhere else.
//
// IT IS A STRUCT AND NOT THREE CLOSURES because the closures were the hole. A
// body handed a closure returning a record could hand one back that read
// anything at all; a source is chosen once, by enter(), and both of its reads
// name r.id.
//
// The two reads differ only in the barrier, and that is the whole of the
// difference between the two entry points: First takes one when withJob asked
// for one, and Again ALWAYS takes one, because the only caller of a second read
// is mend's delete branch, which is exactly the branch that must not decide on
// entries this node has not applied.
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
//
// IT HAS NO id FIELD, AND THAT IS THE EIGHTH ROUND. It used to carry the job id
// beside the holding, as an ordinary string in an ordinary struct — so
// `other := *j; other.id = neighbour` was a legal expression that produced a
// live handle pointing at somebody else's rows, and a dereference copy is not a
// composite literal, so no scan over this package saw it. The id is inside the
// holding now, where this package can read it and cannot write it.
type held struct {
	s  *Service
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

// enter is the ONE PLACE A HANDLE IS MADE and the ONE PLACE A SUBJECT IS NAMED,
// which is what makes "the handle is the section, and the section is about one
// job" true rather than nearly true.
func (s *Service) enter(id string, barrier bool, body func(*held) error) error {
	return jobLock.Do(
		jobReader{s: s, id: id, barrier: barrier},
		func(in section.Holding[jobRecord]) error { return body(&held{s: s, in: in}) },
	)
}

// id is the job this section was entered on, and it is a METHOD rather than a
// field: it reads the subject out of the holding, which is another package's
// struct with unexported fields, so there is no assignment in this package that
// points a handle at a different job.
func (h *held) id() string { return h.in.Subject() }

// rec is the record this section read. found is whether there was one.
func (h *held) rec() jobRecord { return h.in.Value() }
func (h *held) found() bool    { return h.in.Found() }

// reread takes the barrier and reads THIS JOB'S RECORD again, under this same
// holding — mend's second read, and the only one in this package.
//
// It takes nothing, because the read is the source's and the source is enter's.
// A body that wanted to install a value from anywhere else has no argument to
// put it in and no field to assign it to.
func (h *held) reread() error { return h.in.Reread() }

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

// ── THE ENTRY ────────────────────────────────────────────────────────────
//
// entry is one replicated entry under construction, AND EVERY ROW IN IT IS
// COMPOSED HERE — from the handle's own subject, except for the single row a
// job owns that is not keyed by its id, which is composed from the record the
// section read and is named as the exception where it is built.
//
// IT REPLACES commit(ops []raft.BatchOp). That parameter was the last thing in
// this package that a live, unforgeable, correctly-sectioned handle could point
// at another job: `set(jobRecordKey(neighbour(h.id)), value)`, one line inside
// a literal, in a method already on every writer list, passed every static
// guard and every runtime refusal — because the holding IS live, which was
// always the point. There is no such parameter now. A body names a ROW and the
// entry builds the key.
//
// THE OPS ARE NEVER HANDED IN AND NEVER HANDED OUT. Every row this package
// makes is made by one of the row methods below, on a key that method composed
// from the handle, and there is no method here that takes a key at all.
// population_test.go enumerates that from the other side.
//
// AND THE NINTH ROUND IS THE ELEMENT TYPE. The parameter was gone and the rows
// were composed, and a row was still an ordinary raft.BatchOp — an exported
// struct with exported fields — so one line inside stated.anchor,
//
//	s.ops = append(s.ops, raft.BatchOp{Set: &raft.SetCommand{
//	        Key: refLastKey("github.com/evil/repo", "refs/heads/main"), …}})
//
// added a second row to that entry naming ANOTHER REPOSITORY'S ref anchor, and
// the whole service stayed green: every guard over the rows is keyed on which
// FUNCTION was called, and composing a struct calls nothing.
//
// AND THE TENTH IS THE SLICE. The element type became row.Op, whose one field is
// unexported in a package this one cannot reach into, and that line stopped
// compiling — but []row.Op was still an ordinary slice and row.Ops(rows) was
// still an ordinary call, so commit's own argument to Store.Batch was an
// EXPRESSION, and the guard on it read the outermost callee's name:
//
//	store.Batch(append(row.Ops(e.ops), forged), t)   caught, "append"
//	store.Batch(row.Ops(append(e.ops, forged)), t)   GREEN,  "Ops"
//
// One file, +8/-1 lines, measured on the committed tree with all nine of the
// doc's ends intact, put a row moving another repository's ref anchor on every
// replicated entry this service writes. So the rows are a row.Entry now: a
// struct whose slice is unexported, whose rows are MADE INTO it, and which hands
// them to a store without handing them to anybody. There is no expression left
// at that door for a name to be read off.
type entry struct {
	h   *held
	ops row.Entry
	// err carries the one failure that can happen while an entry is being
	// built — a record that will not encode — to commit(), so that a chain
	// reads as one expression instead of five branches. Nothing is written on
	// the way to a commit, so an entry that failed to build has done nothing.
	err error
}

// stated is an entry that CARRIES THIS JOB'S RECORD, and it is a separate type
// on purpose.
//
// A row this entry ADDS is a claim about the record it is adding: "open for
// this repository", "belongs to this project", "finished at this second",
// "this ref now diffs from that commit". None of those can be true of an entry
// that writes no record, so none of them is a method on entry — which makes
// them a compile error there rather than something a nil check catches.
//
// The two methods that stay on entry are the two that undo a row a PREVIOUS
// entry wrote: dropRetained, which retry uses, and dropOrphanedOpen, which is
// mend's and is the one transition that genuinely needs the other shape — it
// deletes an open row whose record is GONE.
type stated struct {
	*entry
	rec jobRecord
}

// writes opens an entry about this job. It is the only constructor there is.
func (h *held) writes() *entry {
	// FOUR IS THE WIDEST ENTRY THIS PACKAGE WRITES — a terminal record, its
	// open row, its retention row and the ref anchor — so the rows below never
	// regrow the slice a body composes them into.
	return &entry{h: h, ops: row.Sized(4)}
}

// THERE IS NO put(key) AND NO drop(key). There was, for one draft, and it was
// the same defect one floor down: a method on the entry that took a key was a
// method any body holding a handle could call with any key at all, and
// `j.writes().put(refLastKey("github.com/evil/repo", "refs/heads/main"), sha)`
// inside ends() left the whole suite GREEN. A door scoped by the SHAPE of its
// argument is a door anybody can walk through. So the rows below each compose
// their own key and add it to the entry themselves — e.ops.Set(…), which is the
// only expression that makes a row at all — and population_test.go enumerates
// which methods may do so from the other side. THAT ENUMERATION HOLDS A NAME:
// what it counts is who names Set and Delete.

// record writes THIS JOB'S record.
//
// THE KEY IS THE HANDLE'S AND SO IS THE RECORD'S OWN id. A body decides what a
// record SAYS — that is the whole of a read-modify-write — but it does not
// decide which job it is: rec.ID is overwritten with the subject before it is
// encoded, so a body that hands over a record it read somewhere else writes
// this job, with foreign contents, rather than writing another job's row.
// begin's entry was a row at jobRecordKey(rec.ID) inside withJob(d.ID, …) until
// the eighth round, where the two agreeing was a property of the store rather
// than of the expression.
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

// retained is the retention row, at the second THIS ENTRY'S record says the job
// finished — which is the same instant the record's UpdatedAt carries, because
// they are written together.
func (s stated) retained() stated {
	s.ops.Set(terminalIndexKey(time.Unix(s.rec.UpdatedAt, 0), s.h.id()), nil)
	return s
}

// dropRetained removes the retention row a PREVIOUS finish wrote, and it is
// keyed off the record THIS SECTION READ rather than the one being written.
//
// THAT IS THE POINT OF DERIVING IT. retry reopens a finished job, and the row
// to delete is the one written when it finished — the timestamp the record
// still carried on the way in. Spelled by hand it had to be read out before the
// record was mutated, or the delete named a key nothing wrote and left the real
// row behind: a job that reads Open and is also listed as retained. Here the
// mutation cannot reach it.
func (e *entry) dropRetained() *entry {
	e.ops.Delete(terminalIndexKey(time.Unix(e.h.rec().UpdatedAt, 0), e.h.id()))
	return e
}

// anchor is the "before" of the next build of this ref: the commit this job
// built. It is the ONE ROW THIS JOB OWNS THAT IS NOT KEYED BY ITS id —
// (repository, ref) is the key, because the thing that reads it is the NEXT
// build of that ref, which is a different job and cannot look under this one —
// so it is the one place "about what" cannot be the subject.
//
// IT IS STILL NOT A KEY OR A VALUE A BODY CHOOSES. All three of its parts come
// off the record THIS SECTION READ, rather than off the record the entry is
// writing: the two differ only by a status and a timestamp on the live path,
// and taking them from the read is what keeps a body from anchoring a ref it
// merely put in a record. It is a method on stated so that the anchor and the
// success it belongs to cannot end up in different entries.
//
// A SECTION THAT FOUND NO RECORD HAS NOTHING TO ANCHOR, and says so rather than
// writing "build/index/ref///last" — a row under two empty segments, which no
// build of anything would ever read back.
func (s stated) anchor() stated {
	if !s.h.found() {
		s.err = errInternal("job %s: a ref anchor decided on a record this section did not find", s.h.id())
		return s
	}
	s.ops.Set(refLastKey(s.h.rec().RepositoryKey, s.h.rec().Ref), []byte(s.h.rec().CommitSHA))
	return s
}

// dropOrphanedOpen is THE NAMED EXCEPTION: the one row this handle cannot
// compose for itself.
//
// mend deletes the open-index row of a job whose record is gone or finished,
// and when it is GONE there is no record to take the repository off. What is
// left is the row reconcile listed, whose two halves parseOpenIndexKey reads
// back out of it — so the repository comes in as an argument and the id, which
// is the half that says WHICH JOB, is still the handle's.
//
// Its one caller is enumerated in population_test.go, which is what keeps this
// from becoming a general-purpose door: an exemption scoped by the shape of its
// argument rather than by its justification is an exemption anybody can reach.
func (e *entry) dropOrphanedOpen(repositoryKey string) *entry {
	e.ops.Delete(openIndexKey(repositoryKey, e.h.id()))
	return e
}

// commit makes the entry, on behalf of the read that decided it, and is THE
// path to a replicated batch — the only one. It was one of two: the sink's door
// was the other, named as an exception in population_test.go because a log row
// was content that no record read decided. The log is a file now (output.go)
// and there is no second path, so the exception is gone rather than narrowed.
//
// IT TAKES NO ROWS AND IT HANDS NONE OVER. `what` is a word for the failure
// message; the rows are the ones this entry's own methods composed, and
// row.Entry.Apply takes the store rather than the batch. The line it replaces —
// store.Batch(row.Ops(e.ops), timeout) — had an argument, and an argument is
// something a body can respell.
//
// what is the gerund the failure is spelled with: "writing job X", "reopening
// job X". The job is the handle's, so a message cannot name a different one
// either.
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

// ── THE WRITES THAT ARE NOT ENTRIES ──────────────────────────────────────

// push gives this job an item, on behalf of the read that decided it should
// have one.
func (h *held) push() error {
	return h.under(func() error { return h.s.work.Push(h.id(), nil) })
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
// AND THE DELIVERY MUST BE THIS JOB'S. A queue item is named by the job id, so
// the one argument here that could point somewhere else is checked against the
// subject rather than trusted — the same clause as every key below, in the one
// write whose target is a value rather than a key. It is this process's own
// fault when it fails, so it is Internal and not Unavailable.
//
// settle acked BEFORE its read until two rounds ago, because queue.Ack barriers
// and its section had no other one. That ordering was real and it is now paid
// for rather than worked around: settle takes withJob's barrier like every
// other section, and its ack is an ordinary write inside the body. It costs one
// replicated entry per settled build — barrier_test.go spends it — and it is
// what makes "every write is the body's" true with no exception.
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
		err := h.s.work.Remove(h.id())
		if err == nil || errors.Is(err, queue.ErrNotFound) || errors.Is(err, queue.ErrClaimed) {
			return nil
		}
		return errUnavailable("withdrawing job %s from the queue: %v", h.id(), err)
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
// THE KEY IS THE SUBJECT'S, like every other write here. A map in this process
// is keyed the same way a row in the store is, and it was keyed by a field
// anything in this package could reassign until the id moved into the holding.
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
		h.s.running[h.id()] = stop
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
		end, ok := h.s.running[h.id()]
		h.s.runningMu.Unlock()
		if ok {
			end()
		}
		return nil
	})
}

// ── THE TRANSITIONS' SHARED ENTRIES ──────────────────────────────────────

// ends builds the entry that finishes this job: the record, the open index row
// removed, the retention index row written. A job is never in a state where it
// is finished but still listed as open, or listed as open but has no record.
//
// IT ALSO CARRIES WHAT THE RUN PRODUCED, which is a field of the record rather
// than a row beside it: one cid for the attempt's log and one per resource
// built. A transition with no run behind it hands over the zero value and
// changes nothing.
//
// IT HANDS BACK AN ENTRY RATHER THAN MAKING ONE so that settle can add the ref
// anchor to the SAME entry — "this job succeeded" and "this is now the commit
// to diff from" cannot end up disagreeing. What it can add is still only a row
// composed from this job: the `extra ...raft.BatchOp` this replaces was the one
// parameter in this file through which a correctly-held handle could name
// another job's rows.
func (h *held) ends(status buildv1.JobStatus, reason string, p produced) stated {
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
func (h *held) finish(status buildv1.JobStatus, reason string) error {
	return h.ends(status, reason, produced{}).commit("finishing")
}

// fail ends this job as Failed and records what the run that failed produced.
//
// THE OUTPUT OF A FAILED BUILD IS THE OUTPUT SOMEBODY WANTS. v1 keeps the log
// of a build that failed only if the failure was one it RETURNED from: the
// publish is a `defer` in the run, so a worker that died skipped it and the
// file it wrote stayed on that node with no cid naming it.
func (h *held) fail(reason string, p produced) error {
	return h.ends(buildv1.JobStatus_JobStatusFailed, reason, p).commit("finishing")
}

// succeed ends this job, records what it produced, AND moves the ref anchor,
// in one entry, because they are the same fact: this commit is what the next
// build of this ref diffs from, and it is read from here rather than from a
// webhook (build.proto: "read from its own job history, never from the
// frontend").
func (h *held) succeed(p produced) error {
	return h.ends(buildv1.JobStatus_JobStatusSuccess, "", p).anchor().commit("finishing")
}

// logged records what a run produced WITHOUT MOVING THE STATUS, and changes
// nothing else about the record.
//
// It is the cancel case first: a cancelled job must not be overwritten with the
// outcome of the run that was cancelled, but the cid of what that run printed
// is the one thing whoever cancelled it wants, and it is a field on the record
// this section already read. v1 needed a second RPC in the other direction to
// carry it (monkey's cancelHandler hands patrick the accumulated logs so
// patrick can write them beside the status).
//
// AND IT IS THE REOPENED JOB'S TOO. A cancel and a retry landing while the log
// went to the store leave a record that is Open on a new attempt — not
// terminal, and not this run's — and the same sentence applies for the same
// reason: the status is not this run's to move and the log is still somebody's
// to read. What makes both safe is that a log is filed under the RUN'S attempt
// and never the record's (output.go's produced), so nothing here can land on a
// line another run owns.
//
// A run that produced nothing writes nothing: an entry with no rows is refused
// by row.Entry.Apply anyway, and a replicated entry that changes no field is a
// cost with no fact in it.
func (h *held) logged(p produced) error {
	if p.logCID == "" && len(p.assets) == 0 {
		return nil
	}
	return h.writes().record(h.rec().with(p)).commit("recording the output of")
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
