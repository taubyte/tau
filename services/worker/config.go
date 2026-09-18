package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
)

// config.go declares what this service is built from. Every interface here is
// declared by the consumer and is as narrow as the use.
//
// THERE IS NO peer.Node IN THIS PACKAGE. It answers on a host and reads from a
// register; it does not join a swarm of its own, hold a datastore or bootstrap
// a DHT, and taking the bundle to get at one of them would make the service
// impossible to construct in a test without all of it.

// Register is the slice of the register this service needs: two reads, and no
// publish.
//
// A WORKER CANNOT PUBLISH, and the interface is where that is enforced rather
// than in a comment. Publishing is what a build does; a worker that could move
// a pointer could deploy, and nothing about running a request needs to.
//
// Both *register.Register (this node's own) and *services/register.Client (one
// on another peer) satisfy it, which is the whole point of the sentinel
// mapping in that client: a node that runs both services wires them together
// with no network, and neither this package nor its tests can tell.
type Register interface {
	Resolve(ctx context.Context, project, environment string) (register.Pointer, error)
	Object(ctx context.Context, id register.ObjectID) ([]byte, error)
}

// Pool is the slice of pkg/invoke this service needs: two calls, one per thing
// a workload can be asked to do.
//
// Everything about warm instances, concurrency, cold starts and scale-to-zero
// is behind it and none of it is this service's business — which is why the
// interface is what this service CALLS and not the pool's whole surface.
//
// TRANSFORM IS HERE AND NOT ON A SECOND INTERFACE because it is the same
// pooling: a transformer is a workload that runs per request, so it wants warm
// slots for the same reason a handler does. What differs is only the answer.
type Pool interface {
	Do(ctx context.Context, spec invoke.Spec, req *invoke.Request) (*invoke.Response, error)
	Transform(ctx context.Context, spec invoke.Spec, req *invoke.Request) (*invoke.Outcome, error)
}

// Config is what an operator declared, and ONLY that: values somebody writes in
// yaml, so it decodes, marshals and round-trips. The injected collaborators are
// not here.
type Config struct {
	// PointerTTL is how long a resolved pointer is trusted before the register
	// is asked again. Default 5s.
	//
	// IT IS THE DEPLOY PROPAGATION BUDGET, and that is the only thing it is.
	// The compiled object a pointer names is immutable and cached forever, so
	// this number does not affect how much work a request does — it affects
	// how long after a deploy the old code keeps being served, and nothing
	// else. Zero would mean a register round trip on every invocation, which
	// is a network hop added to the request path to answer a question whose
	// answer changes on a deploy.
	PointerTTL time.Duration

	// MaxProjects is how many compiled projects to hold at once. Default 64.
	//
	// A cache of content-addressed objects would otherwise never evict: every
	// deploy mints a new one and the old is still valid forever, so "valid" is
	// not a bound. This is.
	MaxProjects int
}

// Pointer is PointerTTL with the default applied.
//
// A CONSUMER READING THE RAW FIELD GETS ZERO whenever an operator left it
// unset, and zero here means "trust nothing, ask every time" — which is the
// opposite of what an unset field asks for and is a network hop added to the
// request path. The same shape bit stats.Config.Fresh, where every register in
// the fleet looked absent because every row was older than nothing.
func (c Config) Pointer() time.Duration {
	if c.PointerTTL <= 0 {
		return DefaultPointerTTL
	}
	return c.PointerTTL
}

// Defaults.
const (
	DefaultPointerTTL  = 5 * time.Second
	DefaultMaxProjects = 64
)

var (
	ErrNoHost     = errors.New("worker: no host to answer on")
	ErrNoRegister = errors.New("worker: a worker with no register does not know what to run")
	ErrNoPool     = errors.New("worker: a worker with no pool has nothing to run it in")
)

// Option injects what this service is built from.
type Option func(*options) error

type options struct {
	register  Register
	pool      Pool
	artifacts Artifacts
	secrets   Secrets
	now       func() time.Time
	meter     Meter

	serverOptions []rpc.ServerOption
}

// WithRegister is where what-to-run comes from. Required.
func WithRegister(r Register) Option {
	return func(o *options) error {
		if r == nil {
			return ErrNoRegister
		}
		o.register = r
		return nil
	}
}

// WithPool is what runs it. Required.
func WithPool(p Pool) Option {
	return func(o *options) error {
		if p == nil {
			return ErrNoPool
		}
		o.pool = p
		return nil
	}
}

// WithClock injects the local clock. Tests use it; production does not.
// WithMeter is where this service says what it accounted for — invocations,
// what each was allowed and how long it took. Nil emits nothing.
func WithMeter(m Meter) Option {
	return func(o *options) error {
		if m == nil {
			return errors.New("worker: a nil meter")
		}
		o.meter = m
		return nil
	}
}

func WithClock(now func() time.Time) Option {
	return func(o *options) error {
		if now == nil {
			return errors.New("worker: a nil clock")
		}
		o.now = now
		return nil
	}
}

func (c Config) withDefaults() (Config, error) {
	if c.PointerTTL == 0 {
		c.PointerTTL = DefaultPointerTTL
	}
	if c.MaxProjects == 0 {
		c.MaxProjects = DefaultMaxProjects
	}
	if c.PointerTTL < 0 {
		return Config{}, fmt.Errorf("worker: pointer ttl %s: a negative deploy budget is not one", c.PointerTTL)
	}
	if c.MaxProjects < 1 {
		return Config{}, fmt.Errorf("worker: %d projects: a cache that holds nothing reloads on every request", c.MaxProjects)
	}
	return c, nil
}
