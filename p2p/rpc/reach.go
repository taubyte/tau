package rpc

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

// reach.go is the rule a composition keeps about every service it reaches: if
// this node runs it, use it directly; otherwise ask who does and dial that one.
//
// It lives here because this is where streams are established. The package
// already owns the pipe and already knows which host it is — RemotePeer is the
// inbound twin of this one, "who called me" to this one's "is the answer me" —
// and the short-circuit is a statement about the pipe: there must be no pipe.
//
// # The short-circuit is structural and not an optimization
//
// When the answer is this host, [Reacher.Now] hands back the local value and
// CONSTRUCTS NO CLIENT AT ALL. v1 reached its own services through "a lame
// client node" pointed at itself, so a call that was already in the right
// process paid a stream, a serialization and a round trip — and every service
// carried its own copy of the decision, so they did not all agree. Nothing in
// here can loop back, because nothing in here dials a peer equal to the host it
// was given.
//
// # What this does not decide
//
// WHO to call is the caller's. Routing by load and freshness is a fleet view's
// business — tau's lives in services/discovery — and a transport package that
// imported a service to answer it would have the dependency backwards. Reach
// takes `pick` as a function; the composition supplies the one that reads its
// view.
//
// # This is not leader forwarding, and the two must not be merged
//
// services/*/forward.go answers a different question: "which peer may WRITE
// this", where the answer is a consensus group's leader, it changes on an
// election, and being wrong is a refused write rather than an extra hop. This
// answers "which node runs this service", where the answer comes from a
// gossiped view, being wrong costs a retry, and the local case is an
// optimization the caller cannot observe. A forward that reached a node because
// it merely RUNS the service would be a write to a follower.

// ErrNoLocalService is the route naming this very host on a node whose local
// half has nothing to hand back.
//
// IT IS A REFUSAL AND NOT A DIAL. The alternative is a stream from this process
// to this process, which is the shape this file exists to end; a caller that
// sees this has a view claiming it serves something it does not, and dialling
// itself would hide that behind a working call.
var ErrNoLocalService = errors.New("the route names this host, which runs no such service")

// Reacher is one service, reached from one host, by whichever of the two arms
// currently applies. It is safe for concurrent use.
//
// The three functions it is built from are each asked at CALL time and not at
// construction: services on a node start concurrently, a node can go, and which
// one answers is a routing question rather than a wiring one.
type Reacher[T any] struct {
	// self is the host this reaches FROM. It is the whole of the short-circuit
	// test: an answer equal to it is an answer already in this process.
	self    peer.ID
	service string

	// routed selects between the two policies. See [Reach] and [ReachRouted].
	routed bool

	pick  func() (peer.ID, error)
	local func() (T, bool)
	dial  func(peer.ID) (T, error)

	// mu guards the one client this holds, opened on first use and re-opened
	// if the peer it was opened for stops being the answer.
	mu     sync.Mutex
	at     peer.ID
	client T
	held   bool
}

// Reach prefers this node's own: `local` is asked first, and when it answers,
// `pick` is never called at all.
//
// A LOCAL SERVICE IS THE ABSENCE OF A HOP AND NOT A CACHE. This is the shape
// for a call on the REQUEST PATH — a read, a send, a secret — where a hop the
// caller did not ask for is latency it cannot see or retry around, and where a
// node that runs the service has by construction the same answer the fleet
// would give.
//
// It is also why a node with no view of a fleet still works: a node that holds
// what it needs never asks who else might.
//
// AN EMPTY ROUTE IS A REFUSAL here, because the local half has already said no
// — `pick`'s error is returned as it came, so the caller's own sentinel
// survives.
//
// `pick` names the peer to call. `local` is this node's own service and the
// bool is whether it runs one. `dial` opens a client onto another node, and is
// the only one of the three that is ever handed a peer; whatever it returns is
// closed by [Reacher.Close] if it has a Close method.
func Reach[T any](h host.Host, service string,
	pick func() (peer.ID, error),
	local func() (T, bool),
	dial func(peer.ID) (T, error),
) *Reacher[T] {
	return newReacher(h, service, false, pick, local, dial)
}

// ReachRouted takes the routed answer even when that is another node: `pick` is
// asked FIRST, and this node's own is used only when the route names it or
// names nobody.
//
// IT IS A DELIBERATE ASYMMETRY AND NOT AN OVERSIGHT TO COPY. Some work exists
// to LEAVE the node that hands it out — a leader distributing a slice of work
// it must not also perform is the case this was written for — and a reacher
// that preferred local would keep every such hand-off on the leader, which is
// the fleet idling while one node works.
//
// AN EMPTY ROUTE MEANS THIS NODE, which is the opposite of [Reach]'s answer and
// is the same sentence a cluster says about voters: a node with no view of a
// fleet is the whole fleet as far as it knows. "Nobody is visible" therefore
// means "nobody but me" rather than "nowhere to send this" — refusing would
// stop this kind of work dead in a single-node cloud. When there is also no
// local half, `pick`'s error is returned as it came.
//
// There are two constructors rather than one policy argument because the two
// have different contracts, not different settings: when `pick` is called, and
// what an empty route means, are both part of what a caller is promising its
// users. A bool at a call site would say neither, and the two paragraphs above
// would have to live in one doc comment with an "if" in the middle.
func ReachRouted[T any](h host.Host, service string,
	pick func() (peer.ID, error),
	local func() (T, bool),
	dial func(peer.ID) (T, error),
) *Reacher[T] {
	return newReacher(h, service, true, pick, local, dial)
}

func newReacher[T any](h host.Host, service string, routed bool,
	pick func() (peer.ID, error),
	local func() (T, bool),
	dial func(peer.ID) (T, error),
) *Reacher[T] {
	r := &Reacher[T]{service: service, routed: routed, pick: pick, local: local, dial: dial}
	if h != nil {
		r.self = h.ID()
	}
	return r
}

// Now is the service, as whichever arm currently applies.
//
// The value is valid for this call and is not held: the routing answer is
// re-read every time, so a caller that kept one would keep whichever node
// answered the first time, forever.
func (r *Reacher[T]) Now(ctx context.Context) (T, error) {
	var zero T

	// A CALLER THAT HAS ALREADY GONE GETS NO STREAM. Everything below may
	// dial, and a dial for a context that is done is a stream opened for an
	// answer nobody will read.
	if err := ctx.Err(); err != nil {
		return zero, err
	}

	// WHO IS ASKED FIRST IS THE WHOLE OF THE POLICY. Reach asks this node,
	// and pick is never called when the answer is yes — so a node holding what
	// it needs works with no view of a fleet at all.
	if !r.routed {
		if v, ok := r.local(); ok {
			return v, nil
		}
	}

	at, err := r.pick()
	if err != nil {
		// AND AN EMPTY ROUTE IS THE OTHER HALF OF IT. ReachRouted falls back to
		// this node because nobody visible means nobody but me; Reach does not,
		// because it already asked and was told no.
		if r.routed {
			if v, ok := r.local(); ok {
				return v, nil
			}
		}
		return zero, err
	}

	// THE SHORT-CIRCUIT. Not a client that happens to loop back — no client.
	if at == r.self {
		if v, ok := r.local(); ok {
			return v, nil
		}
		return zero, fmt.Errorf("%s: %w", r.service, ErrNoLocalService)
	}

	return r.clientAt(at)
}

// At is the peer this reacher currently holds a client to, and whether it holds
// one at all.
//
// It is how a composition asserts the thing that is otherwise invisible: that a
// call which should have stayed in this process opened nothing.
func (r *Reacher[T]) At() (peer.ID, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.at, r.held
}

// Close releases whatever was dialled. A local value is never closed here — it
// belongs to the node that runs the service, not to the reach onto it.
//
// It does not end the reacher: a Now after it opens a new client, which is what
// makes it safe to close a reach that a still-running node may use again.
func (r *Reacher[T]) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.release()
}

// clientAt is the stream to one node's service, kept for as long as that node
// stays the routing answer — so a read on the request path is not a dial on the
// request path.
func (r *Reacher[T]) clientAt(at peer.ID) (T, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.held && r.at == at {
		return r.client, nil
	}

	// THE ANSWER MOVED, so what was dialled for the previous one goes with it.
	// Its error is dropped rather than failing this call: the caller asked to
	// reach a service, and a stream to a node that is no longer the answer
	// failing to close is not that caller's problem.
	r.release()

	c, err := r.dial(at)
	if err != nil {
		var zero T
		return zero, err
	}
	r.client, r.at, r.held = c, at, true
	return c, nil
}

// release drops the held client. The caller holds mu.
func (r *Reacher[T]) release() error {
	if !r.held {
		return nil
	}

	c := r.client
	var zero T
	r.client, r.at, r.held = zero, "", false
	return closeValue(c)
}

// closeValue closes what `dial` returned, whichever shape its Close has.
//
// A GENERATED CLIENT'S Close IS NOT ONE SIGNATURE. connect clients wrapping a
// stream close without answering, others answer with an error, and a T that
// closes in neither way — a bare interface a caller narrowed — is left alone
// rather than refused. The alternative would be a constraint forcing every
// caller to wrap its client in an adapter to satisfy this file.
func closeValue[T any](v T) error {
	switch c := any(v).(type) {
	case interface{ Close() error }:
		return c.Close()
	case interface{ Close() }:
		c.Close()
	}
	return nil
}
