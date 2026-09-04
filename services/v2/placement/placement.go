// Package placement chooses which node runs a request, and is the one place
// that choice is made.
//
// # Why it is its own package
//
// A gateway needs it and so does anything else that hands work to a worker. If
// each of them picked a peer, there would be two routers disagreeing under
// exactly the load that makes routing matter — and the second one would be
// discovered when a node was overloaded, not before.
//
// # What it routes on
//
// LIVE MEASUREMENTS AND NOT A HASH. services/stats already carries what every
// node says about itself — how loaded it is, whether it is ready, and what it
// can answer for cheaply — so the choice is the least loaded node that has the
// project warm, then the least loaded node at all.
//
// The second half is not a fallback so much as the normal case for a project
// nobody has run yet: a worker that holds nothing can still run anything, it
// just fetches the compiled project first. Preferring a warm node is an
// optimisation; requiring one would make a first request impossible.
//
// # A failure to reach a node is not a failure of the request
//
// A node can go while its last report is still fresh — that is what a freshness
// window means — so a connect failure moves to the next candidate. What is NOT
// retried is a request the node answered: a 500 from a workload is that
// workload's answer, and sending it somewhere else would run it twice.
package placement

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/project"
	"github.com/taubyte/tau/pkg/stats"
	"github.com/taubyte/tau/services/worker"
)

// Router is the slice of stats this package needs: one read.
//
// *services/stats.Service satisfies it. The interface is here so that placement
// can be tested against a table of nodes rather than a swarm.
type Router interface {
	Route(name string, maxAge time.Duration) []stats.Node
}

// Invoker is what a chosen node is asked to do. *services/worker.Client
// satisfies it; a local *worker.Service does too, which is what lets a node
// answer its own request without a stream.
type Invoker interface {
	Invoke(ctx context.Context, at project.Address, req *invoke.Request) (*invoke.Response, error)
}

var (
	ErrNoHost    = errors.New("placement: no host to dial from")
	ErrNoRouter  = errors.New("placement: nothing to route from")
	ErrNoWorkers = errors.New("placement: no worker is ready to take this")
)

// Defaults.
const (
	// DefaultMaxAge is how stale a node's report may be and still be routed to.
	// It is the same order as the report interval: a node that has not spoken
	// in this long is one whose load figure is a guess.
	DefaultMaxAge = 30 * time.Second

	// DefaultAttempts is how many nodes are tried before giving up. It is small
	// on purpose: a request that has been refused three times is not going to
	// be taken by the fourth, and the caller is waiting.
	DefaultAttempts = 3
)

// Option configures a Workers.
type Option func(*options) error

type options struct {
	local    Invoker
	maxAge   time.Duration
	attempts int
}

// WithLocal is this node's own worker, used when the router picks this peer.
//
// IT SAVES A STREAM TO ITSELF, and that is all it is: the choice is made the
// same way either way, so a node does not favour itself. Favouring itself is
// how one node in a fleet ends up serving everything it is asked and nothing it
// is not.
func WithLocal(w Invoker) Option {
	return func(o *options) error {
		if w == nil {
			return errors.New("placement: a nil local worker")
		}
		o.local = w
		return nil
	}
}

// WithMaxAge is how stale a node's report may be and still be routed to.
func WithMaxAge(d time.Duration) Option {
	return func(o *options) error {
		if d <= 0 {
			return fmt.Errorf("placement: max age %s: a report is always older than nothing", d)
		}
		o.maxAge = d
		return nil
	}
}

// WithAttempts is how many nodes are tried before giving up.
func WithAttempts(n int) Option {
	return func(o *options) error {
		if n < 1 {
			return fmt.Errorf("placement: %d attempts is not a try", n)
		}
		o.attempts = n
		return nil
	}
}

// Workers routes a request to a worker and runs it there.
type Workers struct {
	host   host.Host
	router Router
	opts   options

	// mu guards the client cache. A client is one libp2p stream multiplexing
	// every call to that peer, so it is worth keeping and worth not opening
	// twice.
	mu      sync.Mutex
	clients map[libp2ppeer.ID]*worker.Client
	closed  bool
}

// New builds a router over a host and a view.
func New(h host.Host, router Router, opts ...Option) (*Workers, error) {
	if h == nil {
		return nil, ErrNoHost
	}
	if router == nil {
		return nil, ErrNoRouter
	}

	o := options{maxAge: DefaultMaxAge, attempts: DefaultAttempts}
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, err
		}
	}

	return &Workers{host: h, router: router, opts: o, clients: map[libp2ppeer.ID]*worker.Client{}}, nil
}

// Invoke runs one request on whichever node should take it.
func (w *Workers) Invoke(ctx context.Context, at project.Address, req *invoke.Request) (*invoke.Response, error) {
	candidates := w.candidates(at)
	if len(candidates) == 0 {
		return nil, ErrNoWorkers
	}

	var last error
	for i, node := range candidates {
		if i >= w.opts.attempts {
			break
		}

		invoker, err := w.invokerFor(node.Peer)
		if err != nil {
			last = err
			continue
		}

		res, err := invoker.Invoke(ctx, at, req)
		if err == nil {
			return res, nil
		}

		// A NODE THAT ANSWERED IS THE ANSWER. Only a failure to reach it is
		// worth another node — anything else is the workload's own outcome,
		// and running it again would run it twice.
		if !unreachable(err) {
			return nil, err
		}
		last = err
	}

	if last == nil {
		last = ErrNoWorkers
	}
	return nil, fmt.Errorf("placement: no worker took %s/%s: %w", at.Kind, at.Name, last)
}

// candidates is the nodes that could take this, warmest first.
//
// The two lists are concatenated rather than merged: within each, stats has
// already ordered by load, and a warm node at higher load is still the better
// choice than a cold one at lower — the fetch it saves is a whole compiled
// project over the wire.
func (w *Workers) candidates(at project.Address) []stats.Node {
	warm := w.router.Route(worker.Descriptor.Name+"/"+at.Project+"/"+at.Environment, w.opts.maxAge)
	any := w.router.Route(worker.Descriptor.Name, w.opts.maxAge)

	held := map[string]bool{}
	for _, n := range warm {
		held[n.Peer] = true
	}

	out := append([]stats.Node(nil), warm...)
	for _, n := range any {
		if !held[n.Peer] {
			out = append(out, n)
		}
	}
	return out
}

// invokerFor is how this node talks to that one: directly when it is this node,
// and over one kept stream otherwise.
func (w *Workers) invokerFor(id string) (Invoker, error) {
	peerID, err := libp2ppeer.Decode(id)
	if err != nil {
		return nil, fmt.Errorf("placement: %q is not a peer: %w", id, err)
	}
	if w.opts.local != nil && peerID == w.host.ID() {
		return w.opts.local, nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil, errors.New("placement: closed")
	}
	if c, ok := w.clients[peerID]; ok {
		return c, nil
	}

	c, err := worker.NewClient(w.host, peerID)
	if err != nil {
		return nil, err
	}
	w.clients[peerID] = c
	return c, nil
}

// Close releases every stream this holds. It is idempotent.
func (w *Workers) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}
	w.closed = true
	for _, c := range w.clients {
		c.Close()
	}
	w.clients = map[libp2ppeer.ID]*worker.Client{}
	return nil
}

// unreachable reports whether a failure was about getting to the node rather
// than about the request.
//
// IT IS A WHITELIST OF SENTINELS AND NOT A STRING MATCH. worker's client maps
// every status it recognises back to a sentinel, so "the deployment is missing
// a piece" and "no route to this peer" are distinguishable — and only the
// second is worth another node.
func unreachable(err error) bool {
	switch {
	case errors.Is(err, worker.ErrNoTarget),
		errors.Is(err, worker.ErrNoRuntime),
		errors.Is(err, worker.ErrAmbiguous),
		errors.Is(err, worker.ErrNoArtifact),
		errors.Is(err, worker.ErrNoSecrets),
		errors.Is(err, invoke.ErrNoRuntime):
		// The deployment is missing a piece. Every node is missing it.
		return false
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		// The caller gave up, or the budget is spent. Another node cannot help.
		return false
	}
	return true
}
