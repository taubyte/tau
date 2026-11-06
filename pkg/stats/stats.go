// Package stats is how loaded everything is, decided against the local clock.
//
// Unlike pkg/membership, whose output is placement and must not be steerable by a
// node's own claims, routing decides from claims: dns and gateway read this, so
// every number is self-reported and steers traffic. A report is a claim. A node
// that overstates its load loses traffic; one that understates it takes traffic it
// cannot serve, caught only by the caller's health checks.
//
// Routing reads are local and never wait for consensus. Each node writes only its
// own row, so no key is written by two nodes.
//
// Observe takes no timestamp, so a peer cannot extend its own validity; age is
// now() minus the local instant the report arrived. A stale load figure is
// confidently wrong, so every row carries Age and Route makes the caller state a
// tolerance.
//
// No pubsub, host, RPC or goroutine: a state machine over Observe, Evict and a
// clock. Load is a number between zero and one the reporter computes; this package
// only compares such numbers.
package stats

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Defaults for Options.
const (
	DefaultFresh  = 15 * time.Second
	DefaultForget = 10 * time.Minute
)

var (
	ErrNoPeer      = errors.New("stats: a report with no peer belongs to nobody")
	ErrStaleRun    = errors.New("stats: report from an older incarnation")
	ErrLoadRange   = errors.New("stats: load must be between 0 and 1")
	ErrForgetShort = errors.New("stats: forget must exceed fresh")
)

// Report is what a node says about itself. It carries no timestamp: see the
// package doc.
type Report struct {
	// Peer is the reporter's identity. A report is authoritative for this
	// peer and for no other.
	Peer string

	// Incarnation distinguishes one run of a peer from the next, exactly as in
	// membership: picked at boot, never lowered, never compared across peers
	// and never converted to a time. It is what makes a report that arrives
	// out of order from a restarted node refusable.
	Incarnation uint64

	// Load is this node's own view of how busy it is, between 0 and 1. What it
	// measures is the reporter's business — see the package doc.
	Load float64

	// Serving is what this node can currently answer for. Routing intersects
	// this with the name being resolved, so a node that has stopped serving
	// something drops out of the answer by reporting a shorter list rather
	// than by anyone having to notice.
	Serving []string

	// Ready is false when the node is up and reporting but does not want
	// requests — draining, warming, or degraded. It is separate from Load
	// because "busy" and "do not send me anything" are different sentences,
	// and a node at 0.99 load is still the right answer when it is the only
	// one serving a name.
	Ready bool

	// Addresses is where clients reach this node, which is not where the fleet reaches
	// it (NAT, a mesh VPN or a container bridge), so it is reported, and is a claim like
	// Load. This package does not parse them.
	Addresses []string

	// Sockets is every L4 proxy this node accepts connections for, and the port each
	// landed on: one port per proxy, since a raw stream carries no name to
	// demultiplex on. A socket's host also belongs in Serving.
	Sockets []Socket

	// FleetRevision is the revision of the cloud's own document this node last
	// computed placement with. It is how a node that is behind becomes visible:
	// placement's other inputs heal on their own, and this is the one every
	// node must agree on.
	FleetRevision uint64

	// DatabasesPending is how many of this node's databases are below their
	// floor as far as it can tell. One number, not a list — the fleet figure is
	// the sum over fresh rows, and which ones is asked of that node directly.
	DatabasesPending int
}

// Socket is one name a client can open a connection to, and where — an SRV
// record with the node left out.
type Socket struct {
	// Host is the complete name a client resolves, e.g. "db.shop.example.com".
	Host string

	// Protocol is the IANA service label, e.g. "postgres" — the first
	// underscore label of an SRV query.
	Protocol string

	// Transport is what carries it, e.g. "tcp" — the second.
	Transport string

	// Port is what the kernel gave that listener on that node.
	Port uint16
}

// Node is one peer's current stats, as this process sees them.
type Node struct {
	Peer        string
	Incarnation uint64
	Load        float64
	Serving     []string
	Ready       bool
	Addresses   []string
	Sockets     []Socket

	FleetRevision    uint64
	DatabasesPending int

	// Age is how long ago this process received the report. It is on every row
	// because a stale load figure is a confidently wrong one, and a caller
	// that cannot see the age cannot tell the difference.
	Age time.Duration
}

// Options are the freshness window, the forget window and the clock.
type Options struct {
	// Fresh is how old a report may be and still be treated as current by
	// Fresh(). Default 15s. It is a default and not a rule: Route makes its
	// caller state a tolerance, because different callers legitimately
	// disagree about what stale means.
	Fresh time.Duration

	// Forget is how long a silent peer is remembered before it is dropped.
	// Default 10m. It must exceed Fresh, or a peer would be forgotten while
	// something could still have been routing to it.
	Forget time.Duration

	// Now is the local clock. Tests inject one; production leaves it nil.
	Now func() time.Time
}

func (o Options) withDefaults() (Options, error) {
	if o.Fresh == 0 {
		o.Fresh = DefaultFresh
	}
	if o.Forget == 0 {
		o.Forget = DefaultForget
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Fresh <= 0 {
		return Options{}, fmt.Errorf("stats: fresh %s: a window that never holds makes every row stale", o.Fresh)
	}
	if o.Forget <= o.Fresh {
		return Options{}, ErrForgetShort
	}
	return o, nil
}

// View is a stats view. It is safe for concurrent use.
//
// Reads take mu shared and write nothing: every request routes through this twice.
// A row silent past Forget is skipped by a read and dropped by the next write.
type View struct {
	opts Options

	mu   sync.RWMutex
	rows map[string]*row

	// names is who serves each name, so a route reads the rows answering for it
	// rather than every row.
	names map[string]map[string]struct{}

	// swept is when forget last ran. A read skips a forgotten row, so dropping
	// one only bounds memory, and a write sweeps at most once a Fresh window.
	swept time.Time
}

type row struct {
	incarnation uint64
	load        float64
	serving     []string
	ready       bool
	addresses   []string
	sockets     []Socket
	fleet       uint64
	pending     int
	received    time.Time
}

// New builds an empty view.
func New(opts Options) (*View, error) {
	o, err := opts.withDefaults()
	if err != nil {
		return nil, err
	}
	return &View{opts: o, rows: map[string]*row{}, names: map[string]map[string]struct{}{}}, nil
}

// Observe records what a peer says about itself, stamped with the local clock.
//
// A report from an older incarnation than the one on record is refused: a
// restarted node's newer report must not be overwritten by one that was in
// flight when it went down.
func (v *View) Observe(r Report) error {
	if r.Peer == "" {
		return ErrNoPeer
	}
	if r.Load < 0 || r.Load > 1 {
		return fmt.Errorf("%w: %v from %s", ErrLoadRange, r.Load, r.Peer)
	}

	now := v.opts.Now()

	// The slice is copied because the caller may reuse the report, and a row
	// that aliased a caller's slice would change under a reader holding the
	// lock's result.
	serving := append([]string(nil), r.Serving...)
	sort.Strings(serving)

	v.mu.Lock()
	defer v.mu.Unlock()
	v.forget(now)

	cur, had := v.rows[r.Peer]
	if had && r.Incarnation < cur.incarnation {
		return fmt.Errorf("%w: %s sent %d, holding %d", ErrStaleRun, r.Peer, r.Incarnation, cur.incarnation)
	}

	next := &row{
		incarnation: r.Incarnation,
		load:        r.Load,
		serving:     serving,
		ready:       r.Ready,
		addresses:   append([]string(nil), r.Addresses...),
		sockets:     append([]Socket(nil), r.Sockets...),
		fleet:       r.FleetRevision,
		pending:     r.DatabasesPending,
		received:    now,
	}
	// A heartbeat mostly repeats what it served, and then the index stands.
	if !had || !slices.Equal(cur.serving, serving) {
		if had {
			v.unindex(r.Peer, cur)
		}
		v.index(r.Peer, next)
	}
	v.rows[r.Peer] = next
	return nil
}

// index and unindex keep names. They are called with mu held.
func (v *View) index(peer string, r *row) {
	for _, name := range r.serving {
		peers := v.names[name]
		if peers == nil {
			peers = map[string]struct{}{}
			v.names[name] = peers
		}
		peers[peer] = struct{}{}
	}
}

func (v *View) unindex(peer string, r *row) {
	for _, name := range r.serving {
		if peers := v.names[name]; peers != nil {
			delete(peers, peer)
			if len(peers) == 0 {
				delete(v.names, name)
			}
		}
	}
}

// Evict drops a peer immediately — it said goodbye, or something else knows it
// is gone.
func (v *View) Evict(peer string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.drop(peer)
}

func (v *View) drop(peer string) {
	if r, ok := v.rows[peer]; ok {
		v.unindex(peer, r)
		delete(v.rows, peer)
	}
}

// Get is one peer's row, however old.
func (v *View) Get(peer string) (Node, bool) {
	now := v.opts.Now()
	v.mu.RLock()
	defer v.mu.RUnlock()
	r, ok := v.rows[peer]
	if !ok || v.forgotten(r, now) {
		return Node{}, false
	}
	return node(peer, r, now), true
}

// All is every remembered row, at any age, ordered by peer.
func (v *View) All() []Node {
	return v.snapshot(func(Node) bool { return true })
}

// Fresh is every row received within the configured freshness window.
func (v *View) Fresh() []Node {
	return v.snapshot(func(n Node) bool { return n.Age <= v.opts.Fresh })
}

// Route is the read that dns and gateway make: who can answer for this name, no
// older than maxAge, least loaded first. The caller states the tolerance, since a
// 60s DNS TTL and a per-request backend pick disagree about stale.
//
// Not-ready nodes are excluded. A busy node is still returned when it is the only
// one serving the name.
func (v *View) Route(name string, maxAge time.Duration) []Node {
	now := v.opts.Now()

	v.mu.RLock()
	defer v.mu.RUnlock()

	var held [32]pick
	picks := v.routable(held[:0], name, now, maxAge)
	out := make([]Node, len(picks))
	var names, sockets int
	for i, p := range picks {
		out[i] = shared(p.peer, p.r, now)
		names += len(p.r.serving) + len(p.r.addresses)
		sockets += len(p.r.sockets)
	}
	return ownAll(out, names, sockets)
}

// RoutePeers is Route's peers alone, in its order, appended to dst: a caller choosing
// a node by identity copies nothing else.
func (v *View) RoutePeers(name string, maxAge time.Duration, dst []string) []string {
	now := v.opts.Now()

	v.mu.RLock()
	defer v.mu.RUnlock()

	var held [32]pick
	for _, p := range v.routable(held[:0], name, now, maxAge) {
		dst = append(dst, p.peer)
	}
	return dst
}

// pick is a row that may take a request, small so that ordering them moves two
// words, not a Node.
type pick struct {
	peer string
	r    *row
}

// routable appends to dst every row serving name that may take a request, least
// loaded first, a tie by peer. It is called with mu held.
func (v *View) routable(dst []pick, name string, now time.Time, maxAge time.Duration) []pick {
	for peer := range v.names[name] {
		r := v.rows[peer]
		if r != nil && !v.forgotten(r, now) && r.ready && now.Sub(r.received) <= maxAge {
			dst = append(dst, pick{peer: peer, r: r})
		}
	}
	slices.SortFunc(dst, func(a, b pick) int {
		return cmp.Or(cmp.Compare(a.r.load, b.r.load), strings.Compare(a.peer, b.peer))
	})
	return dst
}

func (v *View) snapshot(keep func(Node) bool) []Node {
	now := v.opts.Now()

	v.mu.RLock()
	defer v.mu.RUnlock()

	out := make([]Node, 0, len(v.rows))
	var names, sockets int
	for peer, r := range v.rows {
		if v.forgotten(r, now) {
			continue
		}
		if n := shared(peer, r, now); keep(n) {
			out = append(out, n)
			names += len(n.Serving) + len(n.Addresses)
			sockets += len(n.Sockets)
		}
	}
	slices.SortFunc(out, func(a, b Node) int { return strings.Compare(a.Peer, b.Peer) })
	return ownAll(out, names, sockets)
}

// ownAll gives every node its own copies, cut from one backing array for the names
// and one for the sockets: three allocations a call rather than three a node.
func ownAll(out []Node, names, sockets int) []Node {
	strs, socks := make([]string, 0, names), make([]Socket, 0, sockets)
	for i := range out {
		out[i], strs, socks = ownedFrom(out[i], strs, socks)
	}
	return out
}

// ownedFrom is n with its slices copied into the ends of strs and socks, each copy
// capped so an append to it cannot reach the next node's.
func ownedFrom(n Node, strs []string, socks []Socket) (Node, []string, []Socket) {
	at := len(strs)
	strs = append(strs, n.Serving...)
	n.Serving = strs[at:len(strs):len(strs)]
	at = len(strs)
	strs = append(strs, n.Addresses...)
	n.Addresses = strs[at:len(strs):len(strs)]
	from := len(socks)
	socks = append(socks, n.Sockets...)
	n.Sockets = socks[from:len(socks):len(socks)]
	return n, strs, socks
}

func (v *View) forgotten(r *row, now time.Time) bool {
	return now.Sub(r.received) > v.opts.Forget
}

// forget drops every row silent past Forget. It is called with mu held for writing.
func (v *View) forget(now time.Time) {
	if now.Sub(v.swept) < v.opts.Fresh {
		return
	}
	v.swept = now
	for peer, r := range v.rows {
		if v.forgotten(r, now) {
			v.drop(peer)
		}
	}
}

func node(peer string, r *row, now time.Time) Node { return owned(shared(peer, r, now)) }

// shared is a row as a Node still sharing the row's slices, for a filter to read
// before anything is copied.
func shared(peer string, r *row, now time.Time) Node {
	return Node{
		Peer:        peer,
		Incarnation: r.incarnation,
		Load:        r.load,
		Serving:     r.serving,
		Ready:       r.ready,
		Addresses:   r.addresses,
		Sockets:     r.sockets,

		FleetRevision:    r.fleet,
		DatabasesPending: r.pending,

		Age: now.Sub(r.received),
	}
}

// owned gives a Node its own copies of the row's slices, so a caller holding it
// never sees a later report.
func owned(n Node) Node {
	n.Serving = append([]string(nil), n.Serving...)
	n.Addresses = append([]string(nil), n.Addresses...)
	n.Sockets = append([]Socket(nil), n.Sockets...)
	return n
}

// Within is every node reported no longer ago than maxAge, ordered by peer.
func (v *View) Within(maxAge time.Duration) []Node {
	return v.snapshot(func(n Node) bool { return n.Age <= maxAge })
}
