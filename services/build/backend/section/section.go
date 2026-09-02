// Package section makes a write that its own transition did not decide
// IMPOSSIBLE TO SPELL, rather than possible to spell and checked afterwards.
//
// # Why the checks kept failing
//
// Its callers hold a read-modify-write over a replicated store: they read a
// record, decide on what it said, and write it back, and a read is only true
// until something else writes. A mutex serializes them; it does not say WHICH
// holding of itself a write was decided in. Eight guards for that have now been
// written, and the first six each checked a PROXY — a position in a file, a
// spelling, a Lock..Unlock span, a number, and finally a struct nobody outside
// this package could assign a field of — and each was defeated by the next
// shape somebody reached for.
//
// THE SEVENTH FAILED FOR A REASON WORTH KEEPING. It handed the caller a token —
// a value only this package could mint, carrying the holding it was minted in —
// and every write took one. But minting was an EXPORTED, GENERIC function over
// an arbitrary closure, so a caller inside a live holding could stamp anything
// at all with it, including a value it had read somewhere else entirely. A
// check ON a token is a check that a token is present; presence is what you get
// whenever anybody can be handed one.
//
// # So nothing is handed out
//
// There is no Enter, no accessor, and no way to make a live Holding: Do takes
// the lock, takes the read ITSELF, and calls the body with a handle that exists
// for exactly as long as the call. A caller that is not that body has nothing,
// cannot make anything, and therefore cannot write — not because a check
// refuses it, but because the expression it would have to write does not exist.
//
//	Lock     the exclusion, and the only door. Do is its only method that
//	         serializes anything, and it is the only source of a live Holding.
//	Source   WHAT one holding is about and where its value comes from. It is
//	         named at the door, by the call that takes the lock.
//	Holding  one holding of a Lock, the subject it is about, AND the value read
//	         under it. Its zero value holds nothing — it is the only one a
//	         caller can construct — and every write through it is refused.
//
// A body writes by handing Write what it wants done. Write is not a
// "may I?" a caller can ask and then ignore: THE WRITE IS THE ARGUMENT, so
// there is no order to get wrong and no branch to forget.
//
// # The subject travels with the authority, and so does the value
//
// THE EIGHTH ROUND IS THE Source INTERFACE, and it closes two shapes the
// seventh left open.
//
// The first was Reread. It took a closure returning T and installed whatever
// that closure handed back — which is the exported, generic minting function of
// the seventh round wearing a method receiver, needing no token because it was
// a method on the thing that carried the authority. Reread takes nothing now:
// the value it installs comes from Again, on the Source named at the door, and
// a body has nowhere to put a value it got anywhere else.
//
// The second was ABOUT WHAT. A holding said WHEN a write was decided and WHO
// decided it, and nothing at all about WHICH THING it was about — so a caller
// whose subject was an id in a struct beside the holding could copy the handle
// and re-point that id, and write another subject's rows under this subject's
// authority. The subject is a field of the holding now, taken from the Source
// at the instant Do reads it, and every field here is unexported: a caller
// outside this package cannot separate the authority from what it is about,
// because there is no assignment that reaches either.
//
// # What it does not prove, said plainly
//
// A handle is an ordinary Go value. A body can put one in a package variable,
// close over it, or send it to another goroutine, and Go has no way to stop
// that — no lifetime on a parameter, no move semantics, no goroutine identity
// in the standard library. What CAN be pinned is pinned: the handle is
// INVALIDATED when Do returns, so an escaped one refuses every write from that
// instant on, whoever holds the lock meanwhile and however long they hold it.
//
// A copy is still the same holding, subject included — which is what makes
// copying it useless rather than dangerous. Two handles that carry one holding
// are one authority over one subject; there is no assignment that gives a copy
// a different subject, because the copy would have to reach a field of another
// package's struct to make one.
//
// So the split is:
//
//   - THE COMPILER'S HALF. There is no token to obtain, no read to re-stamp, no
//     value to install, no subject to re-point, no lock to leave and retake,
//     and no way for a helper to write without being handed the handle. Each of
//     those is a name that does not exist.
//   - THE RUNTIME'S HALF. A handle that outlives its Do — captured, deferred,
//     or sent elsewhere — is dead, and every write through it is refused with
//     ErrStale rather than made.
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

// Source is WHAT A HOLDING IS ABOUT and where its value comes from. It is
// named at the door, by the call that takes the lock, and never at a write.
//
// IT IS AN INTERFACE RATHER THAN A CLOSURE, and that is the whole of the
// difference from the round before this one. A closure handed to a write site
// is a value the write site chose: Reread(read func() (T, bool, error)) was a
// caller saying "install this", which is the minting function of two rounds ago
// with the token removed because the receiver already carried the authority.
// Everything a holding knows now comes from three methods this package calls
// itself, at instants this package picks.
type Source[T any] interface {
	// Subject names the one thing this holding is about — a job id, a row, a
	// name. It is read ONCE, by Do, and copied into the holding, so a source
	// that changed its mind later would be changing nothing.
	Subject() string
	// First is the read Do takes before it runs the body. Its result is what
	// the body decides on.
	First() (T, bool, error)
	// Again is the read Reread takes, under the same holding. It is a separate
	// method because a section that triages on a cheap read and decides on a
	// second, more careful one is a real shape, and the difference between the
	// two belongs at the door rather than at the write.
	Again() (T, bool, error)
}

// Lock is the exclusion over one table. THE MUTEX IS UNEXPORTED AND NOTHING
// TOUCHES IT ALONE: Do takes it and Do releases it, in one call, so there is no
// way to release the exclusion and keep the identity, and no way to hold the
// identity without having taken the exclusion.
type Lock[T any] struct {
	mu sync.Mutex

	// minted counts holdings and never repeats one, so a handle from a holding
	// that has returned can never match a later one.
	minted atomic.Uint64

	// held is the holding running right now, zero between them. It is read by
	// goroutines that do not hold the mutex — that is the whole point of it —
	// so it is atomic rather than guarded by the mutex beside it.
	//
	// A NUMBER IS WHAT THE ROUND BEFORE LAST WAS DEFEATED THROUGH, and this is
	// not that: what was wrong there was an ACCESSOR that handed one out, so a
	// goroutine holding nothing could spend one. Nothing here returns it, no
	// method takes one, and the field that carries it is unexported — the only
	// way to have this number is to be the body Do is running.
	held atomic.Uint64
}

// Holding is one holding of a Lock, the subject it is about, and the value read
// under it. THE ZERO VALUE IS NO HOLDING AT ALL: it belongs to no Lock, it is
// about nothing, it is live in nothing, and it is the only one a caller outside
// this package can construct. That is the point.
//
// It is handed to Do's body BY VALUE. A copy is the same holding — the liveness
// and the subject it carries are shared, so every copy of an escaped handle
// dies with the one it was copied from and every copy is about the same thing —
// and a copy costs no more than passing the value the read returned, which is
// what a body is here to look at.
type Holding[T any] struct {
	// lock is nil in the zero value, and id stops matching the instant Do
	// returns. Both are refusals, and they are different sentences because they
	// are different mistakes.
	lock *Lock[T]
	id   uint64
	// subject is WHAT this holding is about, snapshotted from the source at the
	// instant of the read. It is a plain string and it is unexported, which
	// together are the whole of the guarantee: a caller can read it and cannot
	// write it, so a handle it copied is about what the original was about.
	subject string
	source  Source[T]
	value   T
	found   bool
}

// Do takes the lock, TAKES THE READ ITSELF, and runs body over what it read.
//
// The read is here rather than in the body because a value the body read for
// itself is a value this package cannot vouch for — which is exactly the hole
// the seventh round left: a stamp handed out is a stamp that can be put on
// anything. Read once, under the lock, and hand over the result.
//
// A read that FAILS runs no body: there is nothing to decide a write on. The
// error is the caller's own and is returned unwrapped.
func (l *Lock[T]) Do(src Source[T], body func(Holding[T]) error) error {
	l.mu.Lock()
	id := l.minted.Add(1)
	l.held.Store(id)
	// AND THIS DEFER IS THE HALF THE COMPILER CANNOT DO. A handle the body
	// captured, deferred or sent to another goroutine is still an ordinary
	// value; what stops it writing is that its holding is over.
	//
	// THE IDENTITY DIES BEFORE THE EXCLUSION IS RELEASED, in that order, so
	// there is no instant at which a write could find a holding that is already
	// on its way out.
	defer func() {
		l.held.Store(0)
		l.mu.Unlock()
	}()

	value, found, err := src.First()
	if err != nil {
		return err
	}

	return body(Holding[T]{
		lock:    l,
		id:      id,
		subject: src.Subject(),
		source:  src,
		value:   value,
		found:   found,
	})
}

// Held answers "is a holding running right now" — WHICH IS A DIFFERENT QUESTION
// FROM "give me one", and is answered with a bool for that reason. A test that
// wants to know whether a write was made under the lock asks this; it cannot
// spend the answer, and there is nothing else here to ask.
func (l *Lock[T]) Held() bool { return l.held.Load() != 0 }

// THE METHODS TAKE A POINTER, which is the one thing about this type that is
// about the machine rather than about the property: a Holding carries whatever
// T is — for its caller a job record, six strings and five integers — and a
// value receiver copies all of it to answer a question about none of it. It
// changes nothing else: no method exposes a field and only Reread writes one.

// Subject is what this holding is ABOUT. It is what a caller composes the keys
// of its writes from, so that a write naming something else has no expression.
func (h *Holding[T]) Subject() string { return h.subject }

// Value is what the read read.
func (h *Holding[T]) Value() T { return h.value }

// Found is whether there was anything there.
func (h *Holding[T]) Found() bool { return h.found }

// Write makes one write, and only while this holding is still the one running.
//
// THE WRITE IS THE ARGUMENT rather than something a caller does after asking
// permission. A gate that answers a question leaves the caller free to ask it
// and write anyway, in the wrong order, or on the branch that forgot; a gate
// that takes the write has no such shape to get wrong.
//
// A REFUSAL IS AN ERROR, NOT A PANIC. A write decided under a holding that has
// ended is a write decided on a read something else was free to invalidate;
// refusing costs whatever redelivery or retry the caller already has a path
// for, and making it costs whatever the stale read was wrong about.
func (h *Holding[T]) Write(w func() error) error {
	if err := h.live(); err != nil {
		return err
	}
	return w()
}

// Reread replaces what this holding carries with a fresh read of THE SAME
// SUBJECT, taken NOW and under this same holding. It is what a body that
// triages on a cheap read and decides on a second one needs.
//
// IT TAKES NOTHING, and that is the fix of the eighth round. It used to take a
// closure returning T and install whatever came back — a caller saying "the
// value is this", under an authority the caller already held, which is the
// minting function of the seventh round with the receiver doing the work the
// token used to. The read is the source's, the source is the door's, and a body
// that wants a value from somewhere else has nowhere to put it.
//
// A failed read leaves what was there. There is no third state to install and
// nothing has happened.
func (h *Holding[T]) Reread() error {
	if err := h.live(); err != nil {
		return err
	}
	value, found, err := h.source.Again()
	if err != nil {
		return err
	}
	h.value, h.found = value, found
	return nil
}

// live is the whole of the runtime half, and it is unexported because a caller
// that could ask it separately could ask it and then write anyway.
func (h *Holding[T]) live() error {
	if h.lock == nil {
		return fmt.Errorf("a write was made through a handle no section ever handed out: %w", ErrStale)
	}
	if h.lock.held.Load() != h.id {
		return fmt.Errorf("a write was made through a handle whose section had returned: %w", ErrStale)
	}
	return nil
}
