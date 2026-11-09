package rpc

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Retry is how a client waits for a service that cannot be reached yet: one that
// no node is found serving, or whose node was found before it began answering. The
// pause between attempts starts at First and doubles up to Max, and a call gives up
// For after it began. A zero field is its default.
//
// It is the node's, one for every client on it, so it is set once in the node's
// config rather than per service.
type Retry struct {
	First time.Duration `yaml:"first,omitempty"`
	Max   time.Duration `yaml:"max,omitempty"`
	For   time.Duration `yaml:"for,omitempty"`
}

// The defaults: a quarter second, doubling to two seconds, for half a minute, which
// is longer than a node takes to start its services.
const (
	DefaultRetryFirst = 250 * time.Millisecond
	DefaultRetryMax   = 2 * time.Second
	DefaultRetryFor   = 30 * time.Second
)

// Defaults is r with every zero field its default.
func (r Retry) Defaults() Retry {
	if r.First == 0 {
		r.First = DefaultRetryFirst
	}
	if r.Max == 0 {
		r.Max = DefaultRetryMax
	}
	if r.For == 0 {
		r.For = DefaultRetryFor
	}
	return r
}

// Check refuses a retry no client could follow.
func (r Retry) Check() error {
	switch {
	case r.First < 0 || r.Max < 0 || r.For < 0:
		return fmt.Errorf("rpc: retry %+v: a negative duration", r)
	case r.First > 0 && r.Max > 0 && r.First > r.Max:
		return fmt.Errorf("rpc: retry first %s exceeds max %s", r.First, r.Max)
	}
	return nil
}

// backoff is one call's pauses.
type backoff struct {
	next     time.Duration
	max      time.Duration
	deadline time.Time
}

func (r Retry) start() *backoff {
	r = r.Defaults()
	return &backoff{next: r.First, max: r.Max, deadline: time.Now().Add(r.For)}
}

// wait pauses before the next attempt, and says false when the call has run out of
// time or its caller has gone.
func (b *backoff) wait(ctx context.Context) bool {
	pause := b.next
	if left := time.Until(b.deadline); left <= 0 {
		return false
	} else if pause > left {
		pause = left
	}
	b.next = min(2*b.next, b.max)
	select {
	case <-ctx.Done():
		return false
	case <-time.After(pause):
		return true
	}
}

// unreached is a call that never reached the service: its stream could not be
// opened, because the peer could not be dialled or does not speak the protocol yet.
// Nothing ran, so it is always safe to try again.
type unreached struct{ err error }

func (u unreached) Error() string { return "rpc: the service was not reached: " + u.err.Error() }
func (u unreached) Unwrap() error { return u.err }

// Unreached reports whether err is a call that never reached its service.
func Unreached(err error) bool {
	var u unreached
	return errors.As(err, &u)
}
