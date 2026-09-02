// Package section makes a write that its own transition did not decide
// IMPOSSIBLE TO SPELL, rather than possible to spell and checked afterwards.
//
// # Why the checks kept failing
//
// Its callers hold a read-modify-write over a replicated store: they read a
// record, decide on what it said, and write it back, and a read is only true
// until something else writes. A mutex serializes them; it does not say WHICH
// holding of itself a write was decided in. Seven guards for that have now been
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
//	Holding  one holding of a Lock AND the value read under it. Its zero value
//	         holds nothing — it is the only one a caller can construct — and
//	         every write through it is refused.
//
// A body writes by handing Write what it wants done. Write is not a
// "may I?" a caller can ask and then ignore: THE WRITE IS THE ARGUMENT, so
// there is no order to get wrong and no branch to forget.
//
// AND THE VALUE TRAVELS WITH THE AUTHORITY, which is what closes the shape the
// round before this one left open. A caller cannot put a live holding on a
// value it read earlier, because the value IS a field of the holding: copying
// the liveness copies the value with it, and every field is unexported, so
// there is no assignment that separates the two. The only way to change what a
// holding carries is Reread, which reads NOW, under this holding — and a caller
// that wants a value from somewhere else has nowhere to put it.
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
// So the split is:
//
//   - THE COMPILER'S HALF. There is no token to obtain, no read to re-stamp,
//     no lock to leave and retake, and no way for a helper to write without
//     being handed the handle. Each of those is a name that does not exist.
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

// Holding is one holding of a Lock and the value read under it. THE ZERO VALUE
// IS NO HOLDING AT ALL: it belongs to no Lock, it is live in nothing, and it is
// the only one a caller outside this package can construct. That is the point.
//
// It is handed to Do's body BY VALUE. A copy is the same holding — the liveness
// it carries is shared, so every copy of an escaped handle dies with the one it
// was copied from — and a copy costs no more than passing the value the read
// returned, which is what a body is here to look at.
type Holding[T any] struct {
	// lock is nil in the zero value, and id stops matching the instant Do
	// returns. Both are refusals, and they are different sentences because they
	// are different mistakes.
	lock  *Lock[T]
	id    uint64
	value T
	found bool
}

// Do takes the lock, TAKES THE READ ITSELF, and runs body over what it read.
//
// The read is here rather than in the body because a value the body read for
// itself is a value this package cannot vouch for — which is exactly the hole
// the round before this one left: a stamp handed out is a stamp that can be put
// on anything. Read once, under the lock, and hand over the result.
//
// A read that FAILS runs no body: there is nothing to decide a write on. The
// error is the caller's own and is returned unwrapped.
func (l *Lock[T]) Do(read func() (T, bool, error), body func(Holding[T]) error) error {
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

	value, found, err := read()
	if err != nil {
		return err
	}

	return body(Holding[T]{lock: l, id: id, value: value, found: found})
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

// Reread replaces what this holding carries with a fresh read, TAKEN NOW AND
// UNDER THIS SAME HOLDING. It is what a body that triages on a cheap read and
// decides on a second one needs, and it is not a way to re-stamp anything: the
// value it installs comes from a read this call makes, inside a holding this
// call has just checked is live, which is the same instant Do's own read has.
//
// A failed read leaves what was there. There is no third state to install and
// nothing has happened.
func (h *Holding[T]) Reread(read func() (T, bool, error)) error {
	if err := h.live(); err != nil {
		return err
	}
	value, found, err := read()
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
