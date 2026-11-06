package worker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
)

// Register is the slice of the register this service needs: two reads and no
// publish, since a worker that could move a pointer could deploy. Both
// *register.Register and *services/register.Client satisfy it.
type Register interface {
	Resolve(ctx context.Context, project, environment string) (register.Pointer, error)
	Object(ctx context.Context, id register.ObjectID) ([]byte, error)
}

// Pool is the slice of pkg/invoke this service calls. Transform is here because a
// transformer is pooled per request like a handler; only the answer differs.
type Pool interface {
	Do(ctx context.Context, spec invoke.Spec, req *invoke.Request) (*invoke.Response, error)
	Transform(ctx context.Context, spec invoke.Spec, req *invoke.Request) (*invoke.Outcome, error)

	// Direct and Shape are the two a PROXY makes: where a connection goes, and
	// what its bytes become on the way. Both are pooled for the reason
	// Transform is — neither answer can be kept.
	Direct(ctx context.Context, spec invoke.Spec, conn *invoke.Connection, peeked []byte) (*invoke.Direction, error)
	Shape(ctx context.Context, spec invoke.Spec, s *invoke.Stream) (*invoke.Shaping, error)
	Route(ctx context.Context, spec invoke.Spec, req *invoke.Request) (*invoke.Routing, error)

	// Conn hands out a connection that outlives the call — the instance is held
	// until the connection closes, not until this returns.
	Conn(ctx context.Context, spec invoke.Spec, port string) (net.Conn, error)
}

// Config is only what an operator declares in yaml; injected collaborators are not
// here.
type Config struct {
	// PointerTTL is how long a resource's built artifact, read from the build
	// records, is trusted before they are read again. Default 5s. A deployment's
	// pointer is not bounded by it: the register reader keeps that until the register
	// says it moved.
	PointerTTL time.Duration

	// MaxProjects is how many compiled projects to hold at once. Default 64.
	//
	// A cache of content-addressed objects would otherwise never evict: every
	// deploy mints a new one and the old is still valid forever, so "valid" is
	// not a bound. This is.
	MaxProjects int

	// Engines is how this node's engines run the code they are handed.
	Engines Engines
}

// Engines is one node's engine tuning, a property of the machine. Empty is each
// engine's default for this arch; an unknown value is refused when the node starts.
type Engines struct {
	// JS is how warpjs runs a js function: "bytecode", "native" or
	// "native+jit". Empty is pkg/invoke/js's DefaultMode, and a native mode on
	// an arch with no native backend runs bytecode.
	JS string

	// Wasm is how wazy runs a component: "compiler" or "interpreter". Empty is
	// the compiler, which interprets on an arch it has no backend for.
	Wasm string
}

// Pointer is PointerTTL with the default applied. The raw field is zero when unset,
// which would mean reading the build records on every request.
func (c Config) Pointer() time.Duration {
	if c.PointerTTL <= 0 {
		return DefaultPointerTTL
	}
	return c.PointerTTL
}

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

// WithMeter is where this service reports invocations, what each was allowed and
// how long it took. Nil emits nothing.
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
