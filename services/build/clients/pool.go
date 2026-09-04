package clients

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/discovery"
	"github.com/taubyte/tau/p2p/rpc"
)

var (
	// ErrNoBackend wraps the last failure when every peer a Resolver named was
	// unavailable. It is what a caller retries on, and it is deliberately the
	// same answer whether the swarm holds no such peer at all or holds several
	// that all said "ask somebody else".
	ErrNoBackend = errors.New("build: no peer answered")

	// ErrClosed is returned by Do after Close.
	ErrClosed = errors.New("build: client pool is closed")
)

// Pool holds at most max rpc.Clients for one protocol and hands them out in
// the order a discovery.Resolver names peers.
//
// A client is an HTTP/2 connection over one libp2p stream, so re-dialling per
// call throws away the multiplexing that makes it worth having; a cache with
// no bound, on the other hand, is a stream per peer the swarm ever mentioned.
// max is that bound, and it is also how many peers one Do will try.
type Pool struct {
	host     host.Host
	resolver discovery.Resolver
	protocol protocol.ID
	max      int

	mu      sync.Mutex
	clients map[peer.ID]*rpc.Client
	order   []peer.ID // insertion order, oldest first — the eviction order
	closed  bool
}

// NewPool prepares calls to whichever peers r names as serving p. It dials
// nothing: the first Do is what opens a stream, and that call's context
// governs it.
func NewPool(h host.Host, r discovery.Resolver, p protocol.ID, max int) (*Pool, error) {
	if h == nil {
		return nil, errors.New("build: no host to dial from")
	}
	if r == nil {
		return nil, errors.New("build: no resolver to name peers with")
	}
	if p == "" {
		return nil, errors.New("build: no protocol to dial")
	}
	if max <= 0 {
		return nil, fmt.Errorf("build: pool size must be positive, got %d", max)
	}
	return &Pool{
		host:     h,
		resolver: r,
		protocol: p,
		max:      max,
		clients:  make(map[peer.ID]*rpc.Client, max),
	}, nil
}

// Protocol is the protocol id this pool dials. An adapter built on a pool
// checks it, so a pool made for build's protocol cannot be handed to something
// that means to call auth.
func (p *Pool) Protocol() protocol.ID { return p.protocol }

// Do calls fn against peers serving the protocol, one at a time, until one
// answers with something other than "ask somebody else".
//
// connect.CodeUnavailable means try the next peer, and it covers three cases
// with one branch: the peer is down, the peer does not serve this protocol at
// all, or the peer is a raft follower and refused because it may not write.
// pool_test.go proves the second of those is genuinely Unavailable rather than
// some transport-shaped error, because the whole reason there is no leader
// lookup anywhere in build is that these three collapse into one branch here.
//
// EVERY OTHER CODE IS THE ANSWER and is returned immediately: a
// FailedPrecondition from the leader is not a routing problem, and retrying it
// against the next peer would turn one considered refusal into N.
//
// A peer that was unavailable is reported to the Resolver and its client is
// dropped, so the next Do does not lead with it.
func (p *Pool) Do(ctx context.Context, fn func(c *rpc.Client) error) error {
	peers, err := p.peers(ctx)
	if err != nil {
		return err
	}

	var last error
	for _, pid := range peers {
		c, err := p.client(pid)
		if err != nil {
			last = err
			continue
		}

		err = fn(c)
		if err == nil {
			return nil
		}
		if connect.CodeOf(err) != connect.CodeUnavailable {
			return err
		}

		last = err
		p.resolver.Failed(pid)
		p.drop(pid)
	}

	// peers guarantees a non-empty list and every iteration above either
	// returns or sets last, so last is never nil here.
	return fmt.Errorf("%w on %s: %w", ErrNoBackend, p.protocol, last)
}

// peers asks the resolver for as many peers as this pool will hold.
func (p *Pool) peers(ctx context.Context) ([]peer.ID, error) {
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}

	peers, err := p.resolver.Peers(ctx, p.max)
	if err != nil {
		return nil, fmt.Errorf("%w: resolving %s: %w", ErrNoBackend, p.protocol, err)
	}
	if len(peers) == 0 {
		return nil, fmt.Errorf("%w: no peer serves %s", ErrNoBackend, p.protocol)
	}
	return peers, nil
}

// client returns the cached client for pid, dialling and caching one if there
// is none. Over max, the oldest is closed and dropped: a peer id names one node
// for good, so a client is safe to keep, but the set of peers is not bounded by
// anything this package controls.
func (p *Pool) client(pid peer.ID) (*rpc.Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, ErrClosed
	}
	if c, ok := p.clients[pid]; ok {
		return c, nil
	}

	c, err := rpc.NewClient(p.host, pid, p.protocol)
	if err != nil {
		return nil, fmt.Errorf("build: dial %s on %s: %w", pid, p.protocol, err)
	}

	p.clients[pid] = c
	p.order = append(p.order, pid)
	for len(p.order) > p.max {
		oldest := p.order[0]
		p.order = p.order[1:]
		if old, ok := p.clients[oldest]; ok {
			old.Close()
			delete(p.clients, oldest)
		}
	}
	return c, nil
}

// drop closes and forgets one peer's client.
func (p *Pool) drop(pid peer.ID) {
	p.mu.Lock()
	defer p.mu.Unlock()

	c, ok := p.clients[pid]
	if !ok {
		return
	}
	c.Close()
	delete(p.clients, pid)
	for i, id := range p.order {
		if id == pid {
			p.order = append(p.order[:i], p.order[i+1:]...)
			break
		}
	}
}

// Close drops every held stream. It is idempotent, and Do afterwards returns
// ErrClosed rather than quietly opening a new stream. It does not close the
// Resolver: the pool was handed one and does not own its lifetime
// (service-conventions.md §2e).
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.closed = true
	for pid, c := range p.clients {
		c.Close()
		delete(p.clients, pid)
	}
	p.order = nil
}
