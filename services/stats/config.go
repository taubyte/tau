package stats

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/taubyte/tau/pkg/stats"
)

// config.go declares what this service is built from, and every interface here
// is declared by the consumer and is as narrow as the use.
//
// THERE IS NO peer.Node ANYWHERE IN THIS PACKAGE, for the reason storage gives
// in the same file: peer.Node bundles a host, a DHT, a datastore, a DAG and
// pubsub, and a test that wanted any one of them got a DHT bootstrap and an
// on-disk datastore with it. What this service needs is somewhere to broadcast
// a small message and somewhere to receive other nodes' broadcasts. That is one
// interface with two methods, and it has an in-memory implementation in this
// package's tests.

// Bus is the slice of publish/subscribe this service needs.
//
// It is deliberately not "a pubsub": no topic management, no peer scoring, no
// join/leave. The service is handed something already scoped to one topic,
// because which topic stats gossips on is a composition decision and not a
// thing this package should be able to get wrong.
type Bus interface {
	// Publish broadcasts one encoded report. A failure is not fatal — the
	// next tick will try again, and every reader already knows how old the
	// rows it holds are.
	Publish(ctx context.Context, data []byte) error

	// Subscribe delivers encoded reports from other nodes until ctx ends.
	// Reports from this node may or may not come back; the view handles its
	// own row arriving either way.
	Subscribe(ctx context.Context) (<-chan []byte, error)
}

// Reporter is what this node knows about itself. It is injected because a
// number this service computed itself would be a number this service has an
// opinion about, and the package doc of pkg/stats says why it must not:
// load means something different per workload, and the node is the only party
// that knows its own.
type Reporter interface {
	Snapshot(ctx context.Context) (Snapshot, error)
}

// Snapshot is one moment of a node's own view of itself.
type Snapshot struct {
	// FleetRevision and DatabasesPending are what this node's own services say
	// about placement — see stats.Report for what each one is for.
	FleetRevision    uint64
	DatabasesPending int

	// Load is between 0 and 1. A Reporter returning anything else is a bug in
	// the Reporter, and it is refused rather than clamped: clamping 1.4 to 1
	// would hide a broken measurement behind a plausible number.
	Load float64

	// Serving is what this node can answer for right now.
	Serving []string

	// Ready is false while draining, warming or degraded.
	Ready bool

	// Addresses is where clients reach this node. It is reported rather than
	// read off the host's peerstore: a peerstore address is how other NODES
	// dial, and behind NAT or a container bridge that is an address no client
	// can use. See the proto field for the full argument.
	Addresses []string

	// Sockets is every L4 proxy this node accepts connections for, and the
	// port each one landed on. Empty says this node is not accepting raw
	// connections, which is better than being routed to and refusing.
	//
	// See the proto field: one port per proxy and not one per node, because a
	// raw stream carries no name to demultiplex on.
	Sockets []stats.Socket
}

// ReporterFunc adapts a function to Reporter.
type ReporterFunc func(context.Context) (Snapshot, error)

func (f ReporterFunc) Snapshot(ctx context.Context) (Snapshot, error) { return f(ctx) }

// Config is what an operator declared, and ONLY that: three durations somebody
// writes in yaml, so it decodes, marshals and round-trips.
//
// THE INJECTED COLLABORATORS ARE NOT HERE. The bus, this node's reporter and the
// clock are handed over at construction with With… , because a live object
// cannot be decoded from a config file. Put to codex@xhigh and agreed:
// "operator configuration is data, not constructor customization".
type Config struct {
	// Interval is how often this node broadcasts. Default 5s.
	//
	// IT IS THE FRESHNESS BUDGET, not a tuning knob in isolation: a reader
	// deciding what age it tolerates is choosing a multiple of this. Fresh
	// defaults to three intervals so two lost reports do not make a healthy
	// node vanish.
	Interval time.Duration

	// Fresh is how old a row may be and still be current. Default 3x Interval.
	Fresh time.Duration

	// Forget is how long a silent peer is remembered. Default 20x Interval,
	// and it must exceed Fresh.
	Forget time.Duration

	// Stabilize is how long a peer must be continuously present before data
	// may be placed on it, and continuously absent before it leaves. Default
	// max(4x Fresh, one minute); it must be at least Fresh.
	//
	// IT IS THE ONLY WAY DOWN FROM THE MINUTE, and the minute is a fleet
	// policy rather than a property of the mechanism: a cloud pays one copy
	// handed over and one copy deleted every time placement moves, so it
	// refuses to move on anything shorter however fast the fleet gossips. A
	// universe is one process on loopback whose nodes do not restart, so it
	// has no thrash to buy off and a minute is a minute of nothing happening —
	// see node/dream, which is the only thing in this repo that sets it.
	Stabilize time.Duration

	// Region is where this node is, broadcast with every report. Empty means a
	// node that will not say. See stats.Report.Region for why this one field is
	// different in kind from everything else a node reports about itself.
	Region string
}

// Option injects what this service is built from.
type Option func(*options) error

type options struct {
	bus      Bus
	reporter Reporter
	now      func() time.Time
}

// WithBus is where reports go and come from. Required.
func WithBus(b Bus) Option {
	return func(o *options) error {
		if b == nil {
			return ErrNoBus
		}
		o.bus = b
		return nil
	}
}

// WithReporter is this node's own numbers. Required.
func WithReporter(r Reporter) Option {
	return func(o *options) error {
		if r == nil {
			return ErrNoReporter
		}
		o.reporter = r
		return nil
	}
}

// WithClock injects the local clock. Tests use it; production does not.
func WithClock(now func() time.Time) Option {
	return func(o *options) error {
		if now == nil {
			return errors.New("stats: a nil clock")
		}
		o.now = now
		return nil
	}
}

// Defaults, expressed as multiples of Interval so the three cannot drift apart.
const (
	DefaultInterval = 5 * time.Second
	freshIntervals  = 3
	forgetIntervals = 20

	// stabilizeFreshWindows is how many freshness windows a peer must be
	// continuously present for before data moves onto it — see Config.Stabilize
	// for why there is a floor under the product.
	stabilizeFreshWindows = 4

	// defaultStabilize is that floor, and it is pkg/membership's own default
	// restated rather than imported: this package decides the window and that
	// one decides what it means.
	defaultStabilize = time.Minute
)

var (
	ErrNoBus      = errors.New("stats: a service with no bus can neither report nor hear")
	ErrNoReporter = errors.New("stats: a service with no reporter has nothing to say about itself")
)

func (c Config) withDefaults() (Config, error) {
	if c.Interval == 0 {
		c.Interval = DefaultInterval
	}
	if c.Interval <= 0 {
		return Config{}, fmt.Errorf("stats: interval %s: a node that never reports is a node nothing can route to", c.Interval)
	}
	if c.Fresh == 0 {
		c.Fresh = freshIntervals * c.Interval
	}
	if c.Forget == 0 {
		c.Forget = forgetIntervals * c.Interval
	}
	if c.Fresh < c.Interval {
		return Config{}, fmt.Errorf("stats: fresh %s is shorter than the %s interval: every row would be stale before its replacement was sent",
			c.Fresh, c.Interval)
	}
	if c.Stabilize == 0 {
		// FOUR FRESHNESS WINDOWS, FLOORED AT THE FLEET POLICY, so a fleet that
		// gossips slowly settles slowly and one that gossips quickly still
		// waits a minute before data moves onto a peer.
		c.Stabilize = stabilizeFreshWindows * c.Fresh
		if c.Stabilize < defaultStabilize {
			c.Stabilize = defaultStabilize
		}
	}
	if c.Stabilize < c.Fresh {
		// pkg/membership refuses this in so many words, and it is refused here
		// too so the operator hears it about the field they wrote rather than
		// about a liveness window they never named.
		return Config{}, fmt.Errorf("stats: stabilize %s is shorter than the %s freshness window: placement would move faster than acknowledgement",
			c.Stabilize, c.Fresh)
	}
	return c, nil
}

func (c Config) viewOptions(now func() time.Time) stats.Options {
	return stats.Options{Fresh: c.Fresh, Forget: c.Forget, Now: now}
}

// Freshness is how old a row may be and still be current under this config,
// with the same defaults New applies.
//
// IT IS EXPORTED BECAUSE A CONSUMER CHOOSING A ROUTING TOLERANCE MUST NOT
// RE-DERIVE IT. A caller that read Config.Fresh directly got zero whenever the
// operator left it unset — every row is older than nothing, so every lookup
// found no candidates and the service it was looking for appeared not to exist.
// That is exactly the failure a defaulted value is supposed to prevent, and the
// only way to prevent it once is to answer the question here.
func (c Config) Freshness() time.Duration {
	defaulted, err := c.withDefaults()
	if err != nil {
		return freshIntervals * DefaultInterval
	}
	return defaulted.Fresh
}
