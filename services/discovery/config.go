package discovery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/taubyte/tau/pkg/stats"
)

// Every interface here is declared by the consumer and as narrow as the use. No
// peer.Node: it bundles a DHT and an on-disk datastore this service does not need.

// Bus is the slice of publish/subscribe this service needs, already scoped to one
// topic, since the topic is a composition decision.
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

// Reporter is what this node knows about itself. Load means something different per
// workload, and only the node knows its own (see pkg/stats).
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

	// Addresses is where clients reach this node, reported rather than read off the
	// peerstore, whose addresses are how other nodes dial.
	Addresses []string

	// Sockets is every L4 proxy this node accepts connections for, and the port each
	// landed on. Empty says this node accepts no raw connections.
	Sockets []stats.Socket
}

// ReporterFunc adapts a function to Reporter.
type ReporterFunc func(context.Context) (Snapshot, error)

func (f ReporterFunc) Snapshot(ctx context.Context) (Snapshot, error) { return f(ctx) }

// Config is what an operator declared: durations that decode from yaml. The bus,
// reporter and clock are injected with With… options, since a live object cannot be
// decoded.
type Config struct {
	// Interval is how often this node broadcasts. Default 5s. It is the freshness
	// budget: Fresh defaults to three intervals so two lost reports do not make a
	// healthy node vanish.
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
	// The minute is fleet policy: every placement move costs a copy handed over and one
	// deleted. instance/dream sets it lower, since a universe's nodes do not restart.
	Stabilize time.Duration
}

// option injects what this service is built from.
type option func(*options) error

type options struct {
	bus      Bus
	reporter Reporter
	now      func() time.Time
}

// WithBus is where reports go and come from. Required.
func withBus(b Bus) option {
	return func(o *options) error {
		if b == nil {
			return ErrNoBus
		}
		o.bus = b
		return nil
	}
}

// WithReporter is this node's own numbers. Required.
func withReporter(r Reporter) option {
	return func(o *options) error {
		if r == nil {
			return ErrNoReporter
		}
		o.reporter = r
		return nil
	}
}

// WithClock injects the local clock. Tests use it; production does not.
func withClock(now func() time.Time) option {
	return func(o *options) error {
		if now == nil {
			return errors.New("discovery: a nil clock")
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
	ErrNoBus      = errors.New("discovery: a service with no bus can neither report nor hear")
	ErrNoReporter = errors.New("discovery: a service with no reporter has nothing to say about itself")
)

func (c Config) withDefaults() (Config, error) {
	if c.Interval == 0 {
		c.Interval = DefaultInterval
	}
	if c.Interval <= 0 {
		return Config{}, fmt.Errorf("discovery: interval %s: a node that never reports is a node nothing can route to", c.Interval)
	}
	if c.Fresh == 0 {
		c.Fresh = freshIntervals * c.Interval
	}
	if c.Forget == 0 {
		c.Forget = forgetIntervals * c.Interval
	}
	if c.Fresh < c.Interval {
		return Config{}, fmt.Errorf("discovery: fresh %s is shorter than the %s interval: every row would be stale before its replacement was sent",
			c.Fresh, c.Interval)
	}
	if c.Stabilize == 0 {
		// Four freshness windows, floored at the fleet policy.
		c.Stabilize = stabilizeFreshWindows * c.Fresh
		if c.Stabilize < defaultStabilize {
			c.Stabilize = defaultStabilize
		}
	}
	if c.Stabilize < c.Fresh {
		// pkg/membership refuses this too; refusing it here names the field the operator
		// wrote.
		return Config{}, fmt.Errorf("discovery: stabilize %s is shorter than the %s freshness window: placement would move faster than acknowledgement",
			c.Stabilize, c.Fresh)
	}
	return c, nil
}

func (c Config) viewOptions(now func() time.Time) stats.Options {
	return stats.Options{Fresh: c.Fresh, Forget: c.Forget, Now: now}
}

// Freshness is how old a row may be and still be current under this config, with
// the same defaults New applies. Consumers choosing a routing tolerance use this
// rather than Config.Fresh, which is zero when unset.
func (c Config) Freshness() time.Duration {
	defaulted, err := c.withDefaults()
	if err != nil {
		return freshIntervals * DefaultInterval
	}
	return defaulted.Fresh
}
