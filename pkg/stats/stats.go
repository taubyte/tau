// Package stats is how loaded everything is, decided against the local clock,
// and it is the half of the old seer that can be lied to.
//
// # Why this is not membership
//
// pkg/membership answers who is here and who should own data, and its doc
// ends by refusing exactly what this package provides:
//
//	There is no capacity, no load and no weight, so there is nothing to lie
//	usefully about.
//
// That refusal is right for membership, whose only output is placement:
// placement must not be steerable by a node's own claims. Routing is the
// opposite — it is a decision made FROM the claims. dns and gateway both read
// this to route, so every number here is self-reported, and every number here
// steers traffic.
//
// So the trust question membership dodged is this package's central one, and
// the answer for now is stated rather than hidden: A REPORT IS A CLAIM. A node
// that overstates its load loses traffic and nothing else breaks; a node that
// understates it takes traffic it cannot serve, and only the caller's own
// health checking catches that. Verified reporting is an interface away, and
// the trusting implementation is the one that ships.
//
// # No consensus, ever
//
// Routing reads must be local and cheap, because routing latency is on every
// request. A view that waited for a quorum before answering "who serves this"
// would put a consensus round in front of DNS. Each node writes only its own
// row — the same rule as storage's usage counter, and the reason this is
// AGENTS-legal: there is no key two nodes can write differently.
//
// # A report carries no clock of its own
//
// Observe takes no timestamp, for the same reason membership's does not: a peer
// that could stamp its own report could extend its own validity forever. Age is
// now() minus the local instant this process received the report.
//
// This matters more here than it does for liveness, because a stale load figure
// is not a missing one — it is a confident wrong one. A node reporting 5% load
// and then wedging keeps attracting traffic for as long as anything trusts the
// last thing it said. So Age is on every row this package hands out, and the
// read that routing uses makes the caller say what age it will tolerate.
//
// # What is not decided here
//
// HOW REPORTS ARRIVE. No pubsub, no host, no RPC, no goroutine — a state
// machine over Observe, Evict and a clock, so "a node that overstates its load
// loses traffic" is a unit test rather than a network fixture.
//
// WHAT LOAD MEANS. It is a number between zero and one that the reporter
// computes about itself. This package compares such numbers; it does not know
// whether they came from CPU, memory, queue depth or a guess, and it must not
// grow an opinion, because the right answer differs per workload and the caller
// is the one who knows.
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

	// Addresses is where CLIENTS reach this node, which is not where the fleet
	// reaches it. A peerstore address is how other nodes dial, and behind NAT,
	// a mesh VPN or a container bridge that is routinely unreachable from the
	// internet — so this is reported rather than derived, and it is a claim in
	// the same sense as Load.
	//
	// This package does not parse them. Whether a string is a usable A record,
	// an AAAA, or a hostname is the reader's question, and a view that had an
	// opinion would be deciding for every reader at once.
	Addresses []string

	// Port is where this node's proxy listens — one per node, since the proxy
	// demultiplexes by name and a workload's own port never leaves the machine.
	// Zero means the node is not accepting ingress yet, which is a thing it is
	// entitled to say.
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
	Port        uint16

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
	port        uint16
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
		port:        r.Port,
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

// Route is the read that dns and gateway make: who can answer for this name,
// no older than maxAge, least loaded first.
//
// THE CALLER STATES THE TOLERANCE. There is no default here on purpose. A DNS
// answer with a 60s TTL and a gateway picking a backend for one request are
// entitled to different definitions of stale, and a package-level default would
// silently impose one of them on the other.
//
// Not-ready nodes are excluded. A node at 0.99 load is still returned when it
// is the only one serving the name — refusing to answer is worse than answering
// with the busy node, and the caller can see the load and decide.
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
		Port:        r.port,
		Age:         now.Sub(r.received),
	}
}
