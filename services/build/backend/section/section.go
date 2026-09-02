// Package section makes "the goroutine making this write is the one that holds
// the section its deciding read was taken in" a fact the COMPILER carries.
//
// # Why it is a package and not a file
//
// Its callers hold a read-modify-write over a replicated store: they read a
// record, decide on what it said, and write it back, and a read is only true
// until something else writes. A mutex serializes them; it does not say WHICH
// holding of itself a write was decided in. Six guards for that have now been
// written in the calling package, and each checked a PROXY — a position in a
// file, a spelling, a Lock..Unlock span, and finally a number — and each was
// defeated by the next shape somebody reached for:
//
//   - a number obtained from a "which section is running" accessor is a token a
//     goroutine that holds NOTHING can spend. The check then proves RECENCY —
//     "a section by this number is running" — and never OWNERSHIP.
//   - a mutex sitting beside the number can be unlocked on its own, which
//     releases the mutual exclusion and leaves the number alive behind it.
//   - a stamp that is a struct field of the caller's own type can be
//     re-assigned, so a read taken in a closed section is re-stamped with a
//     live one and vouches for itself.
//
// None of the three is reachable from outside this package, and that is the
// whole reason it is one: unexported fields of another package's type are not
// assignable, and a type with no exported field and no constructor but Enter
// cannot be forged. What the caller can spend is what this package handed it.
//
// # The three values
//
//	Lock     the exclusion. Enter is the only thing that returns a Section, and
//	         it takes the mutex to make one.
//	Section  one holding of a Lock. Leaving it ends the exclusion AND the
//	         identity in one act, because they are the same act.
//	Reading  one read taken under a Section, with what it read. Immutable: made
//	         only by Read, and no field of it can be assigned by anybody.
//
// A caller that wants to WRITE takes a Reading and asks Live(). A caller that
// holds no Section has no Reading to pass, and cannot make one that answers.
//
// # What it does not prove
//
// It proves the write is made while the section its read was taken in is still
// held, by a caller the section was handed to. It does NOT prove the writing
// goroutine is literally the one that entered: Go has no goroutine identity in
// its standard library, and a holder can hand its Section — or its Reading — to
// another goroutine, exactly as it can hand out anything else it guards. That
// hand-off is a holder passing its own decision along, which is a thing this
// design wants (a write made in a helper carries the read that decided it); a
// caller that was handed nothing has nothing, and the instant the section is
// left every copy of it is dead.
package section

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// ErrStale is what every refusal wraps, so that a caller can tell "the section
// that decided this had closed" — its own fault, identical on every node —
// from a store that refused, which is somebody else's and worth retrying
// elsewhere.
var ErrStale = errors.New("the section that decided it had already been left")

// Lock is a mutex whose holdings are numbered. The mutex is unexported and
// this package has no method that touches it alone, so THE EXCLUSION CANNOT BE
// RELEASED WITHOUT ENDING THE SECTION: Leave is the only path to Unlock, and it
// clears the identity first.
type Lock struct {
	mu sync.Mutex

	// minted counts holdings and never repeats one, so a Section that has been
	// left can never match a later one.
	minted atomic.Uint64

	// held is the holding running right now, zero between them. It is read by
	// goroutines that do not hold the mutex — that is the whole point of it —
	// so it is atomic rather than guarded by the mutex beside it.
	held atomic.Uint64
}

// Section is one holding of a Lock, and it is the only thing a write can be
// decided under. The ZERO VALUE is no section at all: it belongs to no Lock,
// it is live in nothing, and it is what a read that decides no write is stamped
// with. It is the only Section a caller outside this package can construct, and
// that is the point.
type Section struct {
	lock *Lock
	id   uint64
}

// Enter takes the lock and mints the holding. It is the ONLY function in this
// package that returns a live Section, and it cannot return one without having
// taken the mutex first — so a Section in a caller's hand is exclusion it holds
// rather than a number it read.
func (l *Lock) Enter() Section {
	l.mu.Lock()
	id := l.minted.Add(1)
	l.held.Store(id)
	return Section{lock: l, id: id}
}

// Leave ends the section and releases the exclusion, IN THAT ORDER AND AS ONE
// ACT. The identity is cleared before the mutex is released, so there is no
// instant at which a write could find a section that is already on its way out.
//
// It is a compare-and-swap rather than a store because a Section that is no
// longer running must not be able to release the exclusion of the one that is:
// leaving twice, or leaving a section somebody else already left, does nothing
// at all rather than unlocking a mutex this holding does not hold.
func (s Section) Leave() {
	if s.lock == nil || !s.lock.held.CompareAndSwap(s.id, 0) {
		return
	}
	s.lock.mu.Unlock()
}

// Held answers "is a section running right now" — WHICH IS A DIFFERENT
// QUESTION FROM "give me one", and is answered with a bool for that reason. A
// test that wants to know whether a write was made under the lock asks this; it
// cannot spend the answer, and there is nothing else here to ask.
func (l *Lock) Held() bool { return l.held.Load() != 0 }

// Live reports whether this section is still the one running.
func (s Section) Live() error {
	if s.lock == nil {
		return fmt.Errorf("a write decided under no section was made: %w", ErrStale)
	}
	if now := s.lock.held.Load(); now != s.id {
		return fmt.Errorf("a write decided under section %d was made under section %d: %w", s.id, now, ErrStale)
	}
	return nil
}

// Reading is one read AND THE SECTION IT WAS TAKEN IN. Every write its caller
// makes takes one, so "which read decided this write" is an argument the
// compiler carries rather than a sentence in a comment.
//
// IT IS IMMUTABLE, and that is not a convention: every field of it is
// unexported and it is declared here, so no assignment in the calling package
// can re-stamp a reading taken in a section that has closed with the section
// that is running now. Read is the only thing that makes one.
type Reading[T any] struct {
	in    Section
	value T
	found bool
}

// Read takes the read UNDER in and stamps what it returns. The read itself is
// the caller's — this package knows nothing about stores — but the stamp is
// this package's, and it is put on at the instant of the read rather than
// handed to a caller to put on later.
func Read[T any](in Section, take func() (T, bool, error)) (Reading[T], error) {
	value, found, err := take()
	if err != nil {
		return Reading[T]{}, err
	}
	return Reading[T]{in: in, value: value, found: found}, nil
}

// THE THREE ACCESSORS TAKE A POINTER, which is the one thing about this type
// that is about the machine rather than about the property: a Reading holds
// whatever T is — for its caller a job record, which is six strings and five
// integers — and a value receiver copies all of it to answer a question about
// none of it. Measured, that copy was the whole difference between this check
// and the bare atomic load it replaced. It changes nothing else: every field is
// unexported and no method writes one, so a pointer to a Reading is no more
// than a cheaper way to ask.

// Value is what the read read.
func (r *Reading[T]) Value() T { return r.value }

// Found is whether there was anything there.
func (r *Reading[T]) Found() bool { return r.found }

// Live is the whole check a write makes: the section this read was taken in is
// still the section running.
//
// A CALLER REFUSES THE WRITE ON IT rather than panicking. A write decided under
// a section that has been released is a write decided on a read something else
// was free to invalidate; refusing costs whatever redelivery or retry the
// caller already has a path for, and making it costs whatever the stale read
// was wrong about.
func (r *Reading[T]) Live() error { return r.in.Live() }
