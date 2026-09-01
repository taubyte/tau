package discovery

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	libdisc "github.com/libp2p/go-libp2p/core/discovery"
	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/host/eventbus"
)

// DefaultRetryInterval is how often a waiting request makes this package look
// again, and it is the only bound on how long that request waits for a peer
// that is already reachable.
//
// It has to exist because every other source is an event, and the two ways a
// peer arrives without one are both ordinary. A peer can be in the peerstore
// with no event left to fire — it was identified before this Discovery was
// built, or another component wrote the record. And a routing lookup that came
// back empty stays empty for a minute.
//
// That minute is a setting of tau's, not something missing from it: p2p/peer
// wraps the DHT in libp2p's NewBackoffDiscovery, with an exponential backoff
// whose floor is DiscoveryBackoffMin — sixty seconds — so every FindPeers
// inside that window is answered instantly from the first one's empty result,
// without the DHT being asked again. That is the right setting: an empty
// routing table answers empty for everyone, and asking it in a loop costs the
// swarm. What it means here is that the routing half can never be what a
// request waits on, which is why the peerstore is the primary source and
// routing only fills in behind it. This ticker bounds a peerstore that was a
// moment behind, and it does not need routing's window to end to do it.
//
// So it must stay well under the deadline of the call that is waiting. v1's
// number was one second against a ten second send timeout, and one second is
// kept here.
const DefaultRetryInterval = time.Second

// DefaultMaxPeers caps the set. The cap exists because every member is
// protected from the connection manager's trim, and an unbounded set of
// protected peers is an unbounded set of connections the node may not close.
const DefaultMaxPeers = 16

// DefaultLookupLimit is how many peers one routing lookup asks for.
const DefaultLookupLimit = 512

// eventBuffer sizes the subscription. libp2p blocks an emitter whose
// subscriber is behind, so this is slack for a burst of identify completions
// while the loop is mid-scan, not a queue anything is allowed to sit in.
const eventBuffer = 128

var (
	// ErrClosed is returned once Close has been called, or once the context
	// New was given has ended.
	ErrClosed = errors.New("discovery is closed")

	// ErrNoPeers is returned when a request ended with nothing found. It is
	// joined with the context's own error, so a caller can tell a deadline
	// from a cancellation.
	ErrNoPeers = errors.New("no peer serves the protocol")
)

// Resolver is the half of this package a service depends on: which peers can
// answer. Static implements it with a fixed list, which is what a service test
// injects when the question under test is not discovery.
type Resolver interface {
	// Any returns one peer, waiting until there is one or ctx ends.
	Any(ctx context.Context) (peer.ID, error)
	// Peers returns up to want peers, waiting until it has that many or ctx
	// ends. A nil error means at least one peer; a short slice means that is
	// all there was.
	Peers(ctx context.Context, want int) ([]peer.ID, error)
	// Known is the set as it stands, newest information included, without
	// waiting for anything.
	Known() []peer.ID
	// Failed reports that a peer this Resolver named did not work. It is on
	// the interface because the caller is the only one who ever finds out —
	// nothing here connects to anything — and a caller holding a Resolver
	// should not have to know which kind it is to say so.
	Failed(peer.ID)
}

var _ Resolver = (*Discovery)(nil)

// Option adjusts a Discovery before its loop starts.
type Option func(*Discovery)

// WithRouting adds a second source of peers: a content router — the node's
// DHT — that can name peers this host has never connected to. Peers found this
// way must have called Advertise. Without it, discovery sees only what the
// peerstore knows.
func WithRouting(d libdisc.Discoverer) Option {
	return func(dd *Discovery) { dd.routing = d }
}

// WithRetryInterval replaces DefaultRetryInterval. Read that constant before
// raising it: it is what bounds a waiting request.
func WithRetryInterval(d time.Duration) Option {
	return func(dd *Discovery) { dd.retry = d }
}

// WithMaxPeers replaces DefaultMaxPeers.
func WithMaxPeers(n int) Option {
	return func(dd *Discovery) { dd.max = n }
}

// WithLookupLimit replaces DefaultLookupLimit.
func WithLookupLimit(n int) Option {
	return func(dd *Discovery) { dd.limit = n }
}

// Discovery keeps the set of peers serving one protocol.
type Discovery struct {
	host    host.Host
	id      protocol.ID
	ns      string
	routing libdisc.Discoverer

	retry time.Duration
	max   int
	limit int
	tag   string

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	// kick asks the loop to look now. found and lookedUp carry a routing
	// lookup's results and its end.
	kick     chan struct{}
	found    chan peer.AddrInfo
	lookedUp chan struct{}
	// finding is read and written only by the loop goroutine.
	finding bool

	mu         sync.Mutex
	peers      map[peer.ID]*entry
	quarantine map[peer.ID]time.Time
	waiters    map[*waiter]struct{}
}

// entry is what is known about a member beyond the fact that it is one.
type entry struct {
	// advertised records that routing named this peer under the protocol's
	// namespace. It is the only evidence there is for a peer we have never
	// connected to, since the peerstore learns protocols from identify.
	advertised bool
}

// waiter is a request parked inside Peers.
type waiter struct {
	want int
	ch   chan struct{}
}

// New starts watching for peers that serve id. Watching ends with ctx or with
// Close, whichever comes first.
func New(ctx context.Context, h host.Host, id protocol.ID, opts ...Option) (*Discovery, error) {
	if h == nil {
		return nil, errors.New("no host to discover from")
	}
	if id == "" {
		return nil, errors.New("no protocol to discover")
	}

	d := &Discovery{
		host:  h,
		id:    id,
		ns:    namespace(id),
		retry: DefaultRetryInterval,
		max:   DefaultMaxPeers,
		limit: DefaultLookupLimit,
	}
	for _, opt := range opts {
		opt(d)
	}

	switch {
	case d.retry <= 0:
		return nil, errors.New("retry interval must be positive: nothing else bounds how long a request waits")
	case d.max < 1:
		return nil, errors.New("peer cap must be at least 1")
	case d.limit < 1:
		return nil, errors.New("lookup limit must be at least 1")
	}

	sub, err := h.EventBus().Subscribe(
		[]any{
			new(event.EvtPeerIdentificationCompleted),
			new(event.EvtPeerProtocolsUpdated),
			new(event.EvtPeerConnectednessChanged),
		},
		eventbus.BufSize(eventBuffer),
		eventbus.Name("p2p/discovery"),
	)
	if err != nil {
		return nil, fmt.Errorf("subscribing to peer events: %w", err)
	}

	d.ctx, d.cancel = context.WithCancel(ctx)
	d.done = make(chan struct{})
	d.kick = make(chan struct{}, 1)
	d.found = make(chan peer.AddrInfo)
	d.lookedUp = make(chan struct{}, 1)
	d.peers = make(map[peer.ID]*entry)
	d.quarantine = make(map[peer.ID]time.Time)
	d.waiters = make(map[*waiter]struct{})
	// The tag is per Discovery, so two of them watching the same peer do not
	// unprotect each other's connection.
	d.tag = fmt.Sprintf("discovery:%s:%p", id, d)

	go d.run(sub)

	return d, nil
}

// Protocol is what this Discovery is watching for.
func (d *Discovery) Protocol() protocol.ID { return d.id }

// Close stops watching and releases every peer it was protecting. It is safe
// to call more than once, and it returns once the loop has stopped.
func (d *Discovery) Close() {
	d.cancel()
	<-d.done
}

// run is the whole loop. Nothing in it blocks on the network: a routing
// lookup happens in its own goroutine and arrives here as messages, because
// the loop is also the drain for a libp2p event subscription and a slow drain
// stalls the emitter.
func (d *Discovery) run(sub event.Subscription) {
	defer close(d.done)
	defer sub.Close()
	defer d.release()
	// However the loop stops — including a subscription that closed under it
	// — everything downstream of it has to stop too, and a request parked in
	// Peers has to hear that it is waiting for nobody. Registered last so it
	// runs first: release below empties the set, and nobody should learn that
	// the set is empty before learning that there is no loop left to fill it.
	defer d.cancel()

	d.scan()

	retry := time.NewTicker(d.retry)
	defer retry.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return

		case e, ok := <-sub.Out():
			if !ok {
				return
			}
			d.onEvent(e)

		case <-d.kick:
			d.fill()

		case <-retry.C:
			// Two different jobs, and only the first is unconditional.
			// Re-reading the members is cheap and keeps Known honest for a
			// caller that never asks for more. Rescanning the whole peerstore
			// and reissuing a lookup is not, so it happens only for a request
			// that is actually waiting — which is the case this ticker exists
			// for: without it, a request parked when the peerstore was a
			// moment behind, or when a lookup was answered from an empty
			// backoff cache, would never be looked at again.
			d.reap()
			d.fill()

		case ai := <-d.found:
			d.admitFound(ai)

		case <-d.lookedUp:
			d.finding = false
		}
	}
}

// fill looks harder, and only on behalf of a request that is short.
func (d *Discovery) fill() {
	if !d.wanting() {
		return
	}
	d.scan()
	if d.wanting() {
		d.lookup()
	}
}

// onEvent turns any of the three subscribed events into the same question,
// asked about one peer. The events carry more than the peer id — identify
// carries the protocol list, EvtPeerProtocolsUpdated carries the delta — and
// none of it is read on purpose: the peerstore already holds the merged
// answer, so trusting the event's copy would be a second rule that can
// disagree with the first.
func (d *Discovery) onEvent(e any) {
	var pid peer.ID
	switch v := e.(type) {
	case event.EvtPeerIdentificationCompleted:
		pid = v.Peer
	case event.EvtPeerProtocolsUpdated:
		pid = v.Peer
	case event.EvtPeerConnectednessChanged:
		pid = v.Peer
		d.connectedness(pid, v.Connectedness)
	default:
		return
	}
	d.reconsider(pid, false)
}

// connectedness records what a connection coming or going is evidence of.
//
// A live connection is the strongest evidence this package ever gets, and it
// clears any doubt about the peer. Losing one is the only evidence it ever
// gets that a peer may be gone at all, which is why it counts for something:
// the peerstore's protocol record has no expiry and nothing removes it, so a
// node that dies would otherwise be handed out until its addresses aged out
// — fifteen minutes of failed dials on libp2p's schedule. Doubt is the same
// doubt Failed raises, and it lasts the same one interval, because a
// disconnect is just as often a peer restarting as a peer leaving.
func (d *Discovery) connectedness(pid peer.ID, c network.Connectedness) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if c == network.Connected {
		delete(d.quarantine, pid)
		return
	}
	d.quarantine[pid] = time.Now().Add(d.retry)
}

// scan reads the whole peerstore. It is the source that needs nobody to have
// published anything: identify puts a peer's protocols there as soon as we are
// connected to it.
func (d *Discovery) scan() {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, pid := range d.host.Peerstore().Peers() {
		if len(d.peers) >= d.max {
			return
		}
		if _, known := d.peers[pid]; known {
			continue
		}
		if d.admissible(pid, false) {
			d.admitLocked(pid, false)
		}
	}
}

// reap re-asks the question about every member, so a peer that stopped serving
// or went away leaves the set even if its event was missed. It is also where
// an expired quarantine is forgotten, which keeps that map the size of the
// failures still in effect.
func (d *Discovery) reap() {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	for pid, until := range d.quarantine {
		if now.After(until) {
			delete(d.quarantine, pid)
		}
	}
	for pid, e := range d.peers {
		if !d.admissible(pid, e.advertised) {
			d.dropLocked(pid)
		}
	}
}

// reconsider re-asks the question about one peer and admits or drops it.
func (d *Discovery) reconsider(pid peer.ID, advertised bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	e, known := d.peers[pid]
	advertised = advertised || (known && e.advertised)

	switch {
	case d.admissible(pid, advertised):
		d.admitLocked(pid, advertised)
	case known:
		d.dropLocked(pid)
	}
}

// admissible is the single rule for membership, applied identically wherever a
// peer arrives from.
//
// Serving is what the peerstore says: identify's list holding the protocol id.
// An empty list is not a denial — it is a peer we have never identified — and
// for such a peer routing's advertisement is the only evidence there is, so it
// counts. A non-empty list without the id is a denial, which is how a stale
// advertisement is thrown out as soon as we meet the peer it named.
//
// Reachable is connected, or an address still in the peerstore. That second
// half is what stops the set filling with the dead: the protocol book has no
// expiry and nothing ever removes a peer from it, so a node that ran this
// service once would otherwise be handed out forever. Addresses do expire, on
// libp2p's own schedule, and a peer whose last address has gone is a peer
// nothing can dial.
//
// Called with d.mu held.
func (d *Discovery) admissible(pid peer.ID, advertised bool) bool {
	if pid == "" || pid == d.host.ID() {
		return false
	}
	if until, held := d.quarantine[pid]; held && time.Now().Before(until) {
		return false
	}

	protos, err := d.host.Peerstore().GetProtocols(pid)
	if err != nil {
		return false
	}
	if len(protos) == 0 {
		if !advertised {
			return false
		}
	} else if !slices.Contains(protos, d.id) {
		return false
	}

	if d.host.Network().Connectedness(pid) == network.Connected {
		return true
	}
	return len(d.host.Peerstore().Addrs(pid)) > 0
}

// admitLocked adds a peer, or upgrades what is known about one already in.
func (d *Discovery) admitLocked(pid peer.ID, advertised bool) {
	if e, known := d.peers[pid]; known {
		e.advertised = e.advertised || advertised
		return
	}
	if len(d.peers) >= d.max {
		return
	}

	d.peers[pid] = &entry{advertised: advertised}
	// A member is a peer the node is about to talk to, so it is exempt from
	// the connection manager's trim for as long as it is one.
	d.host.ConnManager().Protect(pid, d.tag)

	for w := range d.waiters {
		select {
		case w.ch <- struct{}{}:
		default:
		}
	}
}

func (d *Discovery) dropLocked(pid peer.ID) {
	delete(d.peers, pid)
	d.host.ConnManager().Unprotect(pid, d.tag)
}

// lookup asks routing for peers advertising the namespace. One lookup runs at
// a time: they are minutes apart in effect anyway, since the node's discovery
// is behind a backoff cache, and overlapping them would only multiply the
// goroutines waiting on the same answer.
func (d *Discovery) lookup() {
	if d.routing == nil || d.finding {
		return
	}
	d.finding = true

	go func() {
		defer func() {
			select {
			case d.lookedUp <- struct{}{}:
			case <-d.ctx.Done():
			}
		}()

		// A failed lookup and an empty one are the same thing here — no peers
		// this round — and the retry covers both, so the error has nowhere to
		// go that would not be noise.
		ch, err := d.routing.FindPeers(d.ctx, d.ns, libdisc.Limit(d.limit))
		if err != nil {
			return
		}
		for ai := range ch {
			select {
			case d.found <- ai:
			case <-d.ctx.Done():
				return
			}
		}
	}()
}

// admitFound takes a routing result. The addresses go into the peerstore
// first, at the same TTL a host uses for the addresses it is handed to dial:
// long enough for the caller to reach the peer, short enough that a peer which
// has gone falls back out of the set on its own.
func (d *Discovery) admitFound(ai peer.AddrInfo) {
	if len(ai.Addrs) > 0 {
		d.host.Peerstore().AddAddrs(ai.ID, ai.Addrs, peerstore.TempAddrTTL)
	}
	d.reconsider(ai.ID, true)
}

// release drops the whole set, unprotecting as it goes.
//
// It wakes nobody, on purpose. run cancels d.ctx before this runs, and Peers
// selects on that context, so every parked request has already been told the
// loop is gone by the time the set is emptied. A wake here would be a second
// way to say the same thing that could only ever arrive second.
func (d *Discovery) release() {
	d.mu.Lock()
	defer d.mu.Unlock()

	for pid := range d.peers {
		d.dropLocked(pid)
	}
}

// wanting reports whether any parked request is still short.
func (d *Discovery) wanting() bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	for w := range d.waiters {
		if w.want > len(d.peers) {
			return true
		}
	}
	return false
}

// Known is the set as it stands. Peers the host is connected to come first, so
// the id a caller takes first is one that needs no dial.
func (d *Discovery) Known() []peer.ID {
	d.mu.Lock()
	ids := make([]peer.ID, 0, len(d.peers))
	for pid := range d.peers {
		ids = append(ids, pid)
	}
	d.mu.Unlock()

	connected := make(map[peer.ID]bool, len(ids))
	for _, pid := range ids {
		connected[pid] = d.host.Network().Connectedness(pid) == network.Connected
	}
	// Stable over map order, which Go randomises, so callers that take the
	// first id spread over the connected peers instead of agreeing on one.
	slices.SortStableFunc(ids, func(a, b peer.ID) int {
		switch {
		case connected[a] == connected[b]:
			return 0
		case connected[a]:
			return -1
		default:
			return 1
		}
	})
	return ids
}

// Failed reports that pid did not work, and takes it out of the set.
//
// It is not a blacklist. The peer comes back on its own once the retry
// interval has passed, because the ordinary reasons a connection fails are
// temporary — libp2p's dial backoff after two nodes dialled each other at
// once, a connection the far side trimmed, a node restarting — and a set that
// forgot a peer permanently for one of them would hollow out over a day. What
// the interval buys is that the next call does not get the same broken peer
// back immediately and spin on it.
func (d *Discovery) Failed(pid peer.ID) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.quarantine[pid] = time.Now().Add(d.retry)
	if _, known := d.peers[pid]; known {
		d.dropLocked(pid)
	}
}

// Any returns one peer serving the protocol, waiting until there is one or
// until ctx ends.
func (d *Discovery) Any(ctx context.Context) (peer.ID, error) {
	ids, err := d.Peers(ctx, 1)
	if err != nil {
		return "", err
	}
	return ids[0], nil
}

// Peers returns up to want peers, waiting until it has that many or until ctx
// ends. A nil error means at least one peer came back; a slice shorter than
// want means that is all there was before ctx ended, which is the only way to
// learn that a swarm holds fewer nodes than you asked for.
//
// Fan-out is the caller's own loop over what comes back. Nothing here sends
// anything to anyone.
func (d *Discovery) Peers(ctx context.Context, want int) ([]peer.ID, error) {
	if want < 1 {
		return nil, fmt.Errorf("asked for %d peers", want)
	}
	if want > d.max {
		return nil, fmt.Errorf("asked for %d peers, more than the cap of %d this Discovery keeps", want, d.max)
	}

	w := &waiter{want: want, ch: make(chan struct{}, 1)}
	d.mu.Lock()
	d.waiters[w] = struct{}{}
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		delete(d.waiters, w)
		d.mu.Unlock()
	}()

	for {
		if err := d.ctx.Err(); err != nil {
			return nil, ErrClosed
		}
		if ids := d.Known(); len(ids) >= want {
			return ids[:want], nil
		}

		select {
		case d.kick <- struct{}{}:
		default:
		}

		select {
		case <-w.ch:
		case <-ctx.Done():
			if ids := d.Known(); len(ids) > 0 {
				return ids, nil
			}
			return nil, fmt.Errorf("%s: %w: %w", d.id, ErrNoPeers, ctx.Err())
		case <-d.ctx.Done():
			return nil, ErrClosed
		}
	}
}
