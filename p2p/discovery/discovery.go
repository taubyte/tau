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
	msmux "github.com/multiformats/go-multistream"
)

// DefaultRetryInterval is how often a waiting request makes this package look
// again, and the only bound on how long it waits for a peer already reachable.
//
// A peer can be in the peerstore with no event left to fire (identified before this
// Discovery was built, or written by another component). And p2p/peer wraps the DHT
// in a backoff discovery whose floor (DiscoveryBackoffMin) is sixty seconds, so an
// empty lookup stays empty for a minute. Routing therefore only fills in behind the
// peerstore, and this ticker bounds a peerstore that was a moment behind. It must
// stay well under the deadline of the waiting call.
const DefaultRetryInterval = time.Second

// DefaultMaxPeers caps the set, since every member is protected from the
// connection manager's trim.
const DefaultMaxPeers = 16

// DefaultLookupLimit is how many peers one routing lookup asks for.
const DefaultLookupLimit = 512

// askTimeout bounds one ask: a dial when the peer is not connected, and the
// negotiation. It is libp2p's own negotiation timeout.
const askTimeout = 10 * time.Second

// eventBuffer sizes the subscription. libp2p blocks an emitter whose subscriber is
// behind, so this is slack for a burst while the loop is mid-scan.
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
// answer. Static implements it with a fixed list.
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
	// Failed reports that a peer this Resolver named did not work. Only the caller
	// ever finds out, since discovery makes no calls.
	Failed(peer.ID)
}

var _ Resolver = (*Discovery)(nil)

// Option adjusts a Discovery before its loop starts.
type Option func(*Discovery)

// WithRouting adds a second source of peers: a content router (the node's DHT)
// that can name peers this host has never connected to. Peers found this way must
// have called Advertise.
func WithRouting(d libdisc.Discoverer) Option {
	return func(dd *Discovery) { dd.routing = d }
}

// WithRetryInterval replaces DefaultRetryInterval, which bounds a waiting request.
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
	// lookup's results and its end. answered carries a peer that answered on the
	// protocol when it was asked.
	kick     chan struct{}
	found    chan peer.AddrInfo
	lookedUp chan struct{}
	answered chan peer.ID
	// finding is read and written only by the loop goroutine.
	finding bool

	mu         sync.Mutex
	peers      map[peer.ID]*entry
	quarantine map[peer.ID]time.Time
	waiters    map[*waiter]struct{}
	asking     map[peer.ID]bool
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
	d.answered = make(chan peer.ID)
	d.peers = make(map[peer.ID]*entry)
	d.quarantine = make(map[peer.ID]time.Time)
	d.waiters = make(map[*waiter]struct{})
	d.asking = make(map[peer.ID]bool)
	// The tag is per Discovery, so two of them watching the same peer do not
	// unprotect each other's connection.
	d.tag = fmt.Sprintf("discovery:%s:%p", id, d)

	// THE PEERSTORE IS READ BEFORE NEW RETURNS: a Discovery made on first ask is
	// asked at once, and identify has already said what every connected peer serves.
	d.scan()

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

// run is the whole loop. Nothing in it blocks on the network: a routing lookup runs
// in its own goroutine, because the loop drains a libp2p event subscription and a
// slow drain stalls the emitter.
func (d *Discovery) run(sub event.Subscription) {
	defer close(d.done)
	defer sub.Close()
	defer d.release()
	// However the loop stops, parked requests must hear it. Registered last so it runs
	// first: nobody should see the set emptied by release before learning the loop is
	// gone.
	defer d.cancel()

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
			// Re-reading the members is cheap and keeps Known honest. Rescanning the peerstore
			// and reissuing a lookup happens only for a waiting request, which otherwise would
			// never be looked at again.
			d.reap()
			d.fill()

		case ai := <-d.found:
			d.admitFound(ai)

		case <-d.lookedUp:
			d.finding = false

		case pid := <-d.answered:
			d.reconsider(pid, false)
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

// onEvent turns any of the three subscribed events into the same question about one
// peer. The peerstore holds the merged answer; an event's own protocol data only says
// whether the peer is to be asked when the peerstore denies what the event named.
func (d *Discovery) onEvent(e any) {
	var (
		pid  peer.ID
		said bool
	)
	switch v := e.(type) {
	case event.EvtPeerIdentificationCompleted:
		pid, said = v.Peer, slices.Contains(v.Protocols, d.id)
	case event.EvtPeerProtocolsUpdated:
		pid, said = v.Peer, slices.Contains(v.Added, d.id)
	case event.EvtPeerConnectednessChanged:
		pid = v.Peer
		d.connectedness(pid, v.Connectedness)
	default:
		return
	}
	d.reconsider(pid, false)
	if said {
		d.mu.Lock()
		d.doubtLocked(pid)
		d.mu.Unlock()
	}
}

// connectedness records what a connection coming or going is evidence of. A live
// connection clears any doubt. Losing one raises the same one-interval doubt Failed
// does: the protocol record never expires, so a dead node would otherwise be handed
// out until its addresses aged out, and a disconnect is as often a restart.
func (d *Discovery) connectedness(pid peer.ID, c network.Connectedness) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if c == network.Connected {
		delete(d.quarantine, pid)
		return
	}
	d.quarantine[pid] = time.Now().Add(d.retry)
}

// scan reads the whole peerstore: identify puts a connected peer's protocols there
// with nobody publishing anything.
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
// leaves even if its event was missed. A peer whose quarantine ends is asked again
// too: it left the set for one interval, and a caller reading Known with nobody
// waiting would otherwise go without it until some event happened to name it.
func (d *Discovery) reap() {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	for pid, until := range d.quarantine {
		if now.After(until) {
			delete(d.quarantine, pid)
			if d.admissible(pid, false) {
				d.admitLocked(pid, false)
			}
		}
	}
	for pid, e := range d.peers {
		if !d.admissible(pid, e.advertised) {
			d.dropLocked(pid)
			d.doubtLocked(pid)
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
		d.doubtLocked(pid)
	case advertised:
		d.doubtLocked(pid)
	}
}

// doubtLocked asks pid itself whether it serves the protocol, when identify's list
// leaves it out and there is reason to think it serves: it was a member, an identify
// message named it, or routing did. identify applies two messages from one peer at
// once and keeps whichever lands last, so the list can be older than what the peer
// last sent; and the list never expires, so a peer that began to serve after its
// connection closed is denied by what it said before. Nothing corrects either until
// the peer's protocols change while connected, and no caller connects to a peer it
// is never handed.
//
// Called with d.mu held.
func (d *Discovery) doubtLocked(pid peer.ID) {
	if pid == "" || pid == d.host.ID() || d.asking[pid] {
		return
	}
	protos, err := d.host.Peerstore().GetProtocols(pid)
	if err != nil || len(protos) == 0 || slices.Contains(protos, d.id) {
		return
	}
	d.asking[pid] = true
	go d.ask(pid)
}

// ask hands the loop pid if it answers on the protocol. It lets go of pid before
// the answer is in, so a list overwritten again meanwhile can be asked about anew.
func (d *Discovery) ask(pid peer.ID) {
	yes := d.answers(pid)
	d.mu.Lock()
	delete(d.asking, pid)
	d.mu.Unlock()
	if !yes {
		return
	}
	select {
	case d.answered <- pid:
	case <-d.ctx.Done():
	}
}

// answers opens a stream on the protocol to pid, dialling it if need be. The
// peerstore does not list the protocol, so the host negotiates it for real and
// records it there when pid answers. A peer that fails to answer is asked again each
// retry interval while it stays connected; one that refuses has said what identify
// said, and one that cannot be reached has nothing to be asked over.
func (d *Discovery) answers(pid peer.ID) bool {
	for {
		ctx, cancel := context.WithTimeout(d.ctx, askTimeout)
		s, err := d.host.NewStream(ctx, pid, d.id)
		cancel()
		switch {
		case err == nil:
			s.Reset()
			return true
		case errors.Is(err, msmux.ErrNotSupported[protocol.ID]{}), d.host.Network().Connectedness(pid) != network.Connected:
			return false
		}
		select {
		case <-time.After(d.retry):
		case <-d.ctx.Done():
			return false
		}
	}
}

// admissible is the single rule for membership, wherever a peer arrives from.
//
// Serving is identify's list holding the protocol id. An empty list is a peer never
// identified, for which a routing advertisement counts; a non-empty list without the
// id is a denial, which discards a stale advertisement.
//
// Reachable is connected, or an address still in the peerstore. The protocol book
// never expires, but addresses do, so a peer with no address left is dropped.
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

// lookup asks routing for peers advertising the namespace, one at a time: the
// backoff cache makes overlapping lookups pointless.
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

// admitFound takes a routing result. Its addresses go into the peerstore at the TTL
// a host uses for addresses it is handed to dial, so a gone peer falls out alone.
func (d *Discovery) admitFound(ai peer.AddrInfo) {
	if len(ai.Addrs) > 0 {
		d.host.Peerstore().AddAddrs(ai.ID, ai.Addrs, peerstore.TempAddrTTL)
	}
	d.reconsider(ai.ID, true)
}

// release drops the whole set, unprotecting as it goes. It wakes nobody: run has
// already cancelled d.ctx, which Peers selects on.
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

// Failed reports that pid did not work, and takes it out of the set for one retry
// interval. It is not a blacklist: connection failures are usually temporary, and
// the interval only keeps the next call from spinning on the same peer.
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
// ends. A nil error means at least one peer came back; a short slice means that is
// all there was before ctx ended. Fan-out is the caller's loop.
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
