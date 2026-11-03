// Package reach is how a service on one instance asks another: the one on this
// instance when it runs it, otherwise each node that runs it in turn until one
// answers. An answer is final, a refusal included; only a node that could not answer
// is passed over.
package reach

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/libp2p/go-libp2p/core/peer"
)

// ErrNone is a service no node is known to run.
var ErrNone = errors.New("reach: no node is known to run it")

// Reach asks a service through T, one client per node.
type Reach[T any] struct {
	self     peer.ID
	local    func() (T, bool)
	peers    func() []peer.ID
	dial     func(peer.ID) (T, error)
	answered func(error) bool

	mu      sync.Mutex
	clients map[peer.ID]T
	closed  bool
}

// New builds a reach. local is the service on this instance, if it runs one; peers
// is every node known to run it; dial makes a client of one; answered says which
// errors are the service's answer rather than a node that could not give one.
func New[T any](self peer.ID, local func() (T, bool), peers func() []peer.ID, dial func(peer.ID) (T, error), answered func(error) bool) *Reach[T] {
	return &Reach[T]{self: self, local: local, peers: peers, dial: dial, answered: answered, clients: map[peer.ID]T{}}
}

// final is an error to return as it is, without asking another node.
type final struct{ err error }

func (f final) Error() string { return f.err.Error() }
func (f final) Unwrap() error { return f.err }

// Final marks err as the answer, for a call that cannot be repeated on another
// node, such as one that consumed a stream.
func Final(err error) error {
	if err == nil {
		return nil
	}
	return final{err: err}
}

// Do runs ask against the service: locally when it runs here, otherwise against each
// node in turn until one answers.
func (r *Reach[T]) Do(ctx context.Context, ask func(T) error) error {
	if r.local != nil {
		if t, ok := r.local(); ok {
			return unmark(ask(t))
		}
	}

	var errs []error
	for _, id := range r.peers() {
		if id == r.self {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		t, err := r.client(id)
		if err == nil {
			err = ask(t)
			var f final
			if err == nil || errors.As(err, &f) || r.answered(err) {
				return unmark(err)
			}
		}
		errs = append(errs, fmt.Errorf("%s: %w", id, err))
	}
	if len(errs) == 0 {
		return ErrNone
	}
	return fmt.Errorf("reach: no node answered: %w", errors.Join(errs...))
}

func unmark(err error) error {
	var f final
	if errors.As(err, &f) {
		return f.err
	}
	return err
}

func (r *Reach[T]) client(id peer.ID) (T, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var zero T
	if r.closed {
		return zero, errors.New("reach: closed")
	}
	if t, ok := r.clients[id]; ok {
		return t, nil
	}
	t, err := r.dial(id)
	if err != nil {
		return zero, err
	}
	r.clients[id] = t
	return t, nil
}

// Close closes every client it dialled.
func (r *Reach[T]) Close() error {
	r.mu.Lock()
	clients := r.clients
	r.clients, r.closed = nil, true
	r.mu.Unlock()

	var errs []error
	for _, t := range clients {
		switch c := any(t).(type) {
		case io.Closer:
			errs = append(errs, c.Close())
		case interface{ Close() }:
			c.Close()
		}
	}
	return errors.Join(errs...)
}
