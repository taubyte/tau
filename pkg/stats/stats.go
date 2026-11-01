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
	"errors"
	"fmt"
	"sort"
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
type View struct {
	opts Options

	mu   sync.Mutex
	rows map[string]*row
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
	return &View{opts: o, rows: map[string]*row{}}, nil
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

	v.mu.Lock()
	defer v.mu.Unlock()

	if cur, ok := v.rows[r.Peer]; ok && r.Incarnation < cur.incarnation {
		return fmt.Errorf("%w: %s sent %d, holding %d", ErrStaleRun, r.Peer, r.Incarnation, cur.incarnation)
	}

	// The slice is copied because the caller may reuse the report, and a row
	// that aliased a caller's slice would change under a reader holding the
	// lock's result.
	serving := append([]string(nil), r.Serving...)
	sort.Strings(serving)

	v.rows[r.Peer] = &row{
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
	return nil
}

// Evict drops a peer immediately — it said goodbye, or something else knows it
// is gone.
func (v *View) Evict(peer string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.rows, peer)
}

// Get is one peer's row, however old.
func (v *View) Get(peer string) (Node, bool) {
	now := v.opts.Now()
	v.mu.Lock()
	defer v.mu.Unlock()
	v.forget(now)
	r, ok := v.rows[peer]
	if !ok {
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
	return v.sortedByLoad(func(n Node) bool {
		return n.Ready && n.Age <= maxAge && serves(n.Serving, name)
	})
}

func serves(serving []string, name string) bool {
	i := sort.SearchStrings(serving, name)
	return i < len(serving) && serving[i] == name
}

func (v *View) sortedByLoad(keep func(Node) bool) []Node {
	out := v.snapshot(keep)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Load < out[j].Load })
	return out
}

func (v *View) snapshot(keep func(Node) bool) []Node {
	now := v.opts.Now()

	v.mu.Lock()
	defer v.mu.Unlock()
	v.forget(now)

	out := make([]Node, 0, len(v.rows))
	for peer, r := range v.rows {
		n := node(peer, r, now)
		if keep(n) {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Peer < out[j].Peer })
	return out
}

// forget drops rows nothing should still be routing to. The caller holds the
// lock.
func (v *View) forget(now time.Time) {
	for peer, r := range v.rows {
		if now.Sub(r.received) > v.opts.Forget {
			delete(v.rows, peer)
		}
	}
}

func node(peer string, r *row, now time.Time) Node {
	return Node{
		Peer:        peer,
		Incarnation: r.incarnation,
		Load:        r.load,
		Serving:     append([]string(nil), r.serving...),
		Ready:       r.ready,
		Addresses:   append([]string(nil), r.addresses...),
		Sockets:     append([]Socket(nil), r.sockets...),

		FleetRevision:    r.fleet,
		DatabasesPending: r.pending,

		Age: now.Sub(r.received),
	}
}

// Within is every node reported no longer ago than maxAge, ordered by peer.
func (v *View) Within(maxAge time.Duration) []Node {
	return v.snapshot(func(n Node) bool { return n.Age <= maxAge })
}
