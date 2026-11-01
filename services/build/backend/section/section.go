// Package section makes a write its own transition did not decide impossible to
// spell. Do takes the lock, takes the read itself, and calls the body with a
// handle that lives exactly as long as the call:
//
//	Lock     the exclusion and the only door; Do is the only source of a live
//	         Holding
//	Source   what a holding is about and where its value comes from, named at the
//	         door
//	Holding  one holding of a Lock, its subject and the value read under it; the
//	         zero value holds nothing and every write through it is refused
//
// A body writes by handing Write the write itself. The subject is snapshotted
// from the Source and unexported, so a copy is the same authority over the same
// subject, and Reread takes nothing, reading the same subject again. Go cannot
// stop a handle escaping, so an escaped one is invalidated when Do returns and
// every write through it fails with ErrStale.
package section

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// ErrStale wraps every refusal, telling "the deciding section had closed" (this
// process's fault) from a store refusal (worth retrying elsewhere).
var ErrStale = errors.New("the section that decided it had already been left")

// Source is what a holding is about and where its value comes from, named at the
// door. An interface rather than a closure, so every value a holding carries
// comes from methods this package calls at instants it picks.
type Source[T any] interface {
	// Subject names the one thing this holding is about — a job id, a row, a
	// name. It is read ONCE, by Do, and copied into the holding, so a source
	// that changed its mind later would be changing nothing.
	Subject() string
	// First is the read Do takes before it runs the body. Its result is what
	// the body decides on.
	First() (T, bool, error)
	// Again is Reread's read, separate so a section can triage on a cheap read and
	// decide on a careful one.
	Again() (T, bool, error)
}

// Lock is the exclusion over one table. The mutex is unexported and only Do takes
// and releases it, so exclusion and identity cannot be separated.
type Lock[T any] struct {
	mu sync.Mutex

	// minted counts holdings and never repeats one, so a handle from a holding
	// that has returned can never match a later one.
	minted atomic.Uint64

	// held is the holding running now, zero between them; atomic because goroutines
	// not holding the mutex read it. Nothing returns or takes it.
	held atomic.Uint64
}

// Holding is one holding of a Lock, its subject, and the value read under it. The
// zero value is no holding and the only one constructible outside this package.
// Passed by value; copies share liveness and subject.
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

// Do takes the lock, takes the read itself, and runs body over what it read. A
// failed read runs no body and returns the caller's error unwrapped.
func (l *Lock[T]) Do(src Source[T], body func(Holding[T]) error) error {
	l.mu.Lock()
	id := l.minted.Add(1)
	l.held.Store(id)
	// The runtime half: an escaped handle dies here. The identity dies before the
	// exclusion is released, so no write can find a holding on its way out.
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

// Held reports whether a holding is running now, as a bool nobody can spend.
func (l *Lock[T]) Held() bool { return l.held.Load() != 0 }

// Pointer receivers only to avoid copying T.

// Subject is what this holding is ABOUT. It is what a caller composes the keys
// of its writes from, so that a write naming something else has no expression.
func (h *Holding[T]) Subject() string { return h.subject }

// Value is what the read read.
func (h *Holding[T]) Value() T { return h.value }

// Found is whether there was anything there.
func (h *Holding[T]) Found() bool { return h.found }

// Write makes one write, only while this holding is still the one running. The
// write is the argument, so there is no ask-then-write to get wrong. A refusal is
// an error, not a panic.
func (h *Holding[T]) Write(w func() error) error {
	if err := h.live(); err != nil {
		return err
	}
	return w()
}

// Reread replaces the carried value with a fresh read of the same subject under
// this holding. It takes nothing, so no foreign value can be installed; a failed
// read leaves what was there.
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
