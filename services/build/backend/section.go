package backend

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/pkg/v2/queue"
)

// section.go gives jobLock an IDENTITY, and every write this package makes says
// which identity decided it.
//
// THE PROPERTY IS "the write this read decided is made while the SAME section
// is still held". It is not "a lock is held", and it is not "the call sits
// between two tokens in a file" — and that distinction is not academic: five
// successive guards in this package each checked a PROXY for it, and each one
// caught the shape its author had mutated and missed the next one.
//
//   - a table keyed by the READING function could not see a write made by a
//     function that reads nothing (run's two acks);
//   - "the call is after the first jobLock.Lock()" recorded no Unlock at all,
//     so a body that read, released and then wrote was reported as covered;
//   - "the call is inside SOME Lock..Unlock span" cannot see WHICH span, so a
//     body that reads, unlocks, does real work, re-locks and then writes is
//     covered by a span that is not the one its read was taken in;
//   - the pairing scan matched the idents openIndexKey and jobRecordKey, which
//     is a spelling and not a key;
//   - and the behavioural needles TryLock at the write, which finds A lock held
//     and not THE lock that decided the write — a re-taken one answers the same.
//
// No scan over the source can see it, because it is a fact about two instants
// in one execution rather than about two positions in a file. So it is checked
// where it lives, at runtime, and there it is one comparison:
//
//	entering jobLock mints a new identity;
//	a read of a job record records the identity it was taken under;
//	every write asserts that identity is still the one running.
//
// THE FOUR SHAPES THEN FAIL BY CONSTRUCTION rather than by being anticipated:
//
//	Unlock and re-Lock           a new identity, so the recorded one no longer matches
//	a write from a deferred call runs after the unlock, so there is no identity at all
//	a closure invoked outside    the same
//	a write in another function  it is handed the identity or it cannot write
//
// THE STATIC GUARDS STAY, in population_test.go. They are cheap, they fail at
// review time rather than at run time, and they cover the half this cannot: not
// WHEN a write is made but WHICH KEY it writes, and which functions may write
// at all. Neither half is sufficient and neither is redundant.
//
// WHAT IT COSTS. One atomic add and one atomic store per section entered, one
// atomic store per exit, and one atomic load plus a compare per write —
// alongside a mutex acquisition and a replicated raft entry respectively.
// section_test.go's BenchmarkSection measures it rather than asserting it is
// negligible. It is compiled in everywhere: a check that is only in the test
// build is a production binary running code no test ran.

// section names one holding of jobLock. Zero is "no section", which never
// matches anything — a write that carries it is a write no section decided.
type section uint64

// unsectioned is what a read taken outside jobLock is stamped with. job() is
// the one read of a job record in this package that decides no write, so it is
// the one that takes no section — and the stamp is what makes that a fact the
// code states rather than a comment.
const unsectioned section = 0

// sectionLock is jobLock's type: a mutex that also says which holding of itself
// is running right now.
type sectionLock struct {
	mu sync.Mutex

	// minted counts sections. Only the holder increments it.
	minted atomic.Uint64
	// held is the section running right now, zero between them. It is read by
	// goroutines that do NOT hold the mutex — that is the whole point of it —
	// so it is atomic rather than guarded by the mutex beside it.
	held atomic.Uint64
}

// Lock enters a section and returns its identity. Every entry mints a NEW one,
// so a section released and retaken is a different section and the difference
// is a number rather than an argument.
func (l *sectionLock) Lock() section {
	l.mu.Lock()
	return l.enter()
}

// TryLock is Lock for a caller that will not wait — a test asking whether a
// section is held at some instant.
func (l *sectionLock) TryLock() bool {
	if !l.mu.TryLock() {
		return false
	}
	l.enter()
	return true
}

func (l *sectionLock) enter() section {
	at := l.minted.Add(1)
	l.held.Store(at)
	return section(at)
}

// Unlock leaves the section. The identity is cleared BEFORE the mutex is
// released, so there is no instant at which a write could find a section that
// is already on its way out.
func (l *sectionLock) Unlock() {
	l.held.Store(0)
	l.mu.Unlock()
}

// current is the section running right now, or unsectioned.
func (l *sectionLock) current() section { return section(l.held.Load()) }

// errStaleSection is what live() refuses with, and it is a sentinel rather than
// a string because the CODE a refusal answers with matters as much as the
// message — see refused below.
var errStaleSection = errors.New("the section that decided it had already been left")

// live is the whole check: the section this write was decided in is still the
// section running.
//
// IT REFUSES THE WRITE RATHER THAN MAKING IT, and rather than panicking. A
// write decided under a section that has been released is a write decided on a
// read something else was free to invalidate — the defect this package has now
// closed four times. Refusing it costs a redelivery, which this design already
// has a path for; making it costs whatever the stale read was wrong about: a
// cancelled build reported as a success, a reopened job with no queue item, an
// open row deleted out from under a retry.
func (at section) live() error {
	if now := jobLock.current(); at == unsectioned || at != now {
		return errInternal("a write decided under section %d was made under section %d: %w", at, now, errStaleSection)
	}
	return nil
}

// refused spells a write that did not happen, keeping why it did not in the
// wire code and not only in the message.
//
// A STORE THAT REFUSED IS Unavailable — "ask somebody else" — and that is the
// one code services/build/clients' Pool.Do walks past to the next peer
// (leader.go). A SECTION THAT HAD ALREADY CLOSED IS NOT THAT. It is this
// process's own fault, identical on every node in the cloud, and answering it
// Unavailable would send a caller round every peer it knows looking for one
// where the code is different.
func refused(err error, format string, args ...any) error {
	if errors.Is(err, errStaleSection) {
		return errInternal(format+": %v", append(args, err)...)
	}
	return errUnavailable(format+": %v", append(args, err)...)
}

// reading is one read of a job record AND THE SECTION IT WAS TAKEN IN. Every
// write below takes one, so "which read decided this write" is an argument the
// compiler carries rather than a sentence in a comment.
type reading struct {
	rec   jobRecord
	found bool
	at    section
}

// as is the same section carrying a different record: create's push is decided
// by the read that found NOTHING, and queues the record that read let it write.
// It is the only place besides record() where a reading is made, because a
// reading made anywhere else is an identity nothing read.
func (r reading) as(rec jobRecord) reading {
	return reading{rec: rec, found: true, at: r.at}
}

func (r reading) live() error { return r.at.live() }

// commit writes ONE ENTRY on behalf of the read that decided it, and is this
// package's only path to Store.Batch — attemptSink.write excepted, and named as
// the exception in population_test.go, because a log or asset row is content
// keyed by attempt that no record read decides.
func (s *Service) commit(r reading, ops []raft.BatchOp) error {
	if err := r.live(); err != nil {
		return err
	}
	return s.store.Batch(ops, s.cfg.Queue.ApplyTimeout)
}

// push gives a job an item, on behalf of the read that decided it should have
// one.
func (s *Service) push(r reading, id string) error {
	if err := r.live(); err != nil {
		return err
	}
	return s.work.Push(id, nil)
}

// ack removes an item for good.
//
// IT TAKES A SECTION AND NOT A reading, because settle's ack comes BEFORE its
// read rather than after it: the ack is that section's barrier, and the record
// it then reads is decided on the ack having succeeded. begin's and the
// dead-letter drain's acks pass the reading's own section, because there the
// order is the ordinary one and the read is what decided to drop the delivery.
func (s *Service) ack(at section, q *queue.Queue, d queue.Delivery) error {
	if err := at.live(); err != nil {
		return err
	}
	if err := q.Ack(d); err != nil {
		return fmt.Errorf("ack %s: %w", d.ID, err)
	}
	return nil
}
