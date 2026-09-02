package backend

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/pkg/v2/queue"
	"github.com/taubyte/tau/services/build/backend/section"
)

// section.go is where this package's writes are, ALL OF THEM, and every one
// takes a reading — one read of a job record, and the holding of jobLock it was
// taken in (backend/section).
//
// THE PROPERTY IS OWNERSHIP: "the goroutine making this write is the one that
// holds the section its deciding read was taken in". It is not "a lock is
// held", it is not "the call sits between two tokens in a file", and it is not
// "a section by this number is running" — and those distinctions are not
// academic. Six guards in this package have checked a proxy for it, and each
// caught the shape its author had mutated and missed the next one:
//
//   - a table keyed by the READING function could not see a write made by a
//     function that reads nothing (run's two acks);
//   - "the call is after the first jobLock.Lock()" recorded no Unlock at all,
//     so a body that read, released and then wrote was reported as covered;
//   - "the call is inside SOME Lock..Unlock span" cannot see WHICH span, so a
//     body that reads, unlocks, works, re-locks and writes is covered by a span
//     that is not the one its read was taken in;
//   - the pairing scan matched the idents openIndexKey and jobRecordKey, which
//     is a spelling and not a key;
//   - a behavioural needle that TryLocks at the write finds A lock held and not
//     THE lock that decided the write — a re-taken one answers the same;
//   - and a NUMBER read out of the lock ("which section is running") is a token
//     a goroutine holding nothing at all can spend. It proves RECENCY. This is
//     what closed that: there is no accessor that hands one out, so a caller
//     that holds no section has nothing to pass and cannot make one.
//
// WHAT IS COMPILER-ENFORCED NOW, rather than checked:
//
//	a token is got by ENTERING     section.Enter is the only thing that returns
//	                               one, and it takes the mutex to make it
//	the mutex is not beside it     it is unexported in another package, so
//	                               exclusion cannot be released without ending
//	                               the section: Leave is the only path to it
//	a reading cannot be re-stamped every field of it is unexported in another
//	                               package, so `r.at = at` does not compile
//	a write cannot name a section  every helper below takes a reading, so
//	                               "hand the ack a different live section" is
//	                               not an expression this package can write
//
// WHAT IS STILL A GUARD, in population_test.go, because no type can say it:
// which functions may write, which KEY they may write, and that record() is the
// only reader of a job record and section.Read's only caller. They fail at
// review time rather than at run time and they cover the half this cannot.
//
// WHAT IT COSTS. One atomic add and one atomic store per section entered, one
// compare-and-swap per exit, and one atomic load plus a compare per write —
// alongside a mutex acquisition and a replicated raft entry respectively.
// section_test.go's BenchmarkSection measures it rather than asserting it is
// negligible. It is compiled in everywhere: a check that is only in the test
// build is a production binary running code no test ran.

// unsectioned is what a read taken outside jobLock is stamped with. job() is
// the one read of a job record in this package that decides no write, so it is
// the one that takes no section — and the stamp is what makes that a fact the
// code states rather than a comment.
//
// IT IS A FUNCTION AND NOT A VAR because a var is a thing a later body could
// assign a LIVE section to, and this must never become a route to one.
func unsectioned() section.Section { return section.Section{} }

// reading is one read of a job record and the section it was taken in. It is
// section.Reading and not a struct of this package's own, so that NOTHING HERE
// CAN ASSIGN A FIELD OF IT: a reading taken in a section that has closed cannot
// be re-stamped with the section running now, because the field does not exist
// to be assigned. Read is what makes one; record() is Read's only caller.
type reading = section.Reading[jobRecord]

// refused spells a write that did not happen, keeping why it did not in the
// wire code and not only in the message.
//
// A STORE THAT REFUSED IS Unavailable — "ask somebody else" — and that is the
// one code services/build/clients' Pool.Do walks past to the next peer
// (leader.go). A SECTION THAT HAD ALREADY CLOSED IS NOT THAT. It is this
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
func (s *Service) commit(r reading, ops []raft.BatchOp) error {
	if err := r.Live(); err != nil {
		return stale(err)
	}
	return s.store.Batch(ops, s.cfg.Queue.ApplyTimeout)
}

// push gives a job an item, on behalf of the read that decided it should have
// one.
func (s *Service) push(r reading, id string) error {
	if err := r.Live(); err != nil {
		return stale(err)
	}
	return s.work.Push(id, nil)
}

// ack removes an item for good, on behalf of the read that decided it should
// go: begin's read of a job that has already finished, the dead-letter drain's
// of one it has just failed, and settle's of the record this run's outcome is
// about.
//
// IT TAKES A READING AND NOT A SECTION, and that is the whole of the fix for
// the last shape this got wrong. An ack that took a section took whatever
// number was running at the instant of the WRITE, so a body that split the read
// that decided the drop from the ack that carried it out — six lines, two
// functions, two sections — was allowed, and the item was destroyed on a
// decision taken in a section that had closed. There is no longer a way to
// spell it: the argument is the read.
//
// settle acked BEFORE its read until this round, because queue.Ack barriers and
// its section had no other one. That ordering was real and it is now paid for
// rather than worked around: settle takes lead() like every other section, and
// its ack is an ordinary write decided by the read behind that barrier. It
// costs one replicated entry per settled build — barrier_test.go spends it —
// and it is what makes "every ack carries its deciding read" true with no
// exception to write down.
func (s *Service) ack(r reading, q *queue.Queue, d queue.Delivery) error {
	if err := r.Live(); err != nil {
		return stale(err)
	}
	if err := q.Ack(d); err != nil {
		return fmt.Errorf("ack %s: %w", d.ID, err)
	}
	return nil
}

// register records the run the claim's read decided, so that a Cancel
// serialized after that section finds something to stop.
//
// A MAP IN THIS PROCESS IS A WRITE LIKE ANY OTHER, and it goes through the same
// door as the three above for the same reason. Two arguments in this package
// rest their whole weight on this write being made inside the section that
// claimed the job: runner.go's begin (a cancel serialized after the claim must
// find the run registered, or it writes a terminal record while a build it
// could have stopped runs on to its own deadline) and jobs.go's withdraw
// (swallowing queue.ErrClaimed is only honest because the holder's own ack
// always comes, and what makes it prompt is cancel's stopRun reaching a run
// registered here).
//
// Its only guard was a scan that asked whether the assignment sat inside SOME
// jobLock span, which cannot see which span — and a registration handed to
// run() as a closure built inside the section and invoked after begin returns
// is inside the span by every measure a scan has, and outside the section by
// the only one that matters. Handed the read instead, it is refused.
//
// forget is its counterpart and takes no reading: it is not decided by a read
// but by the run being over, it runs outside every section (the build holds no
// lock), and deleting the cancel of a run that has ended is what run's defer is
// for. The claim loop is the only goroutine that claims
// (TestTheClaimLoopIsTheOnlyGoroutineThisPackageStarts), so the entry it drops
// is always the one this same goroutine registered.
func (s *Service) register(r reading, stop context.CancelFunc) error {
	if err := r.Live(); err != nil {
		return errInternal("registering the run of job %s: %w", r.Value().ID, err)
	}
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	s.running[r.Value().ID] = stop
	return nil
}
