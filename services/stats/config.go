package stats

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/taubyte/tau/pkg/v2/stats"
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
// opinion about, and the package doc of pkg/v2/stats says why it must not:
// load means something different per workload, and the node is the only party
// that knows its own.
type Reporter interface {
	Snapshot(ctx context.Context) (Snapshot, error)
}

// Snapshot is one moment of a node's own view of itself.
type Snapshot struct {
	// Load is between 0 and 1. A Reporter returning anything else is a bug in
	// the Reporter, and it is refused rather than clamped: clamping 1.4 to 1
	// would hide a broken measurement behind a plausible number.
	Load float64

	// Serving is what this node can answer for right now.
	Serving []string

	// Ready is false while draining, warming or degraded.
	Ready bool
}

// ReporterFunc adapts a function to Reporter.
type ReporterFunc func(context.Context) (Snapshot, error)

func (f ReporterFunc) Snapshot(ctx context.Context) (Snapshot, error) { return f(ctx) }

// Config is what an operator declared, plus the two things injected by whatever
// composes the node.
type Config struct {
	// Bus is where reports go and come from. Required.
	Bus Bus

	// Reporter is this node's own numbers. Required.
	Reporter Reporter

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

	// Now is the local clock. Tests inject one; production leaves it nil.
	Now func() time.Time
}

// Defaults, expressed as multiples of Interval so the three cannot drift apart.
const (
	DefaultInterval = 5 * time.Second
	freshIntervals  = 3
	forgetIntervals = 20
)

var (
	ErrNoBus      = errors.New("stats: a service with no bus can neither report nor hear")
	ErrNoReporter = errors.New("stats: a service with no reporter has nothing to say about itself")
)

func (c Config) withDefaults() (Config, error) {
	if c.Bus == nil {
		return Config{}, ErrNoBus
	}
	if c.Reporter == nil {
		return Config{}, ErrNoReporter
	}
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
	if c.Now == nil {
		c.Now = time.Now
	}
	return c, nil
}

func (c Config) viewOptions() stats.Options {
	return stats.Options{Fresh: c.Fresh, Forget: c.Forget, Now: c.Now}
}
