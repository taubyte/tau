// Package worker is where code runs. v1 called it substrate.
//
// # What it does, in four steps
//
//	a request names a resource     (project, environment, scope, kind, name)
//	  -> the register says what that environment currently runs
//	    -> the compiled project says what that resource declares about itself
//	      -> pkg/invoke runs it on the tier the config selected
//
// EVERY RUNTIME VALUE COMES FROM STEP THREE. Memory, timeout, concurrency and
// environment are `PerEnv()` in the vocabulary — the author is expected to set
// them differently per environment — so none of them is a constructor argument
// and none is an operator setting. A worker that had captured a memory limit at
// construction would serve a number the author changed, and nothing would
// happen when they changed it.
//
// # It runs nothing itself
//
// Warm instances, cold starts, concurrency and scale-to-zero are pkg/invoke's,
// behind a one-method interface. Which tier a resource runs on is read off the
// vocabulary. So what is left here is the lookup and its two caches, which is
// the part that is actually about being a worker.
//
// # Two things it deliberately cannot do
//
// IT CANNOT PUBLISH. The Register interface it declares has two reads and no
// write, because a worker that could move a pointer could deploy.
//
// IT CANNOT INVENT AN ARTIFACT. A resource whose source is a path in the
// repository names the source, not what came out of building it, and that is
// the build service's to say — see Artifacts in resolve.go.
package worker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/host"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/register/projects"
)

// Service is a worker.
type Service struct {
	host     host.Host
	cfg      Config
	register Register
	pool     Pool

	// artifacts and secrets are optional. Absent, the resources that need them
	// are refused by name rather than run without them.
	artifacts Artifacts
	secrets   Secrets

	// meter is where this service says what it accounted for. Nil is the
	// ordinary case; see meter.go.
	meter Meter

	// now is this service's clock, so a test that moves time moves what an
	// invocation is billed for.
	now func() time.Time

	projects *projects.Cache

	serverOptions []rpc.ServerOption

	startMu sync.Mutex
	server  *rpc.Server

	// streams is the raw half — see stream.go. It is a second listener on the
	// same host rather than a second service, because what it reaches is this
	// service's own pool and resolver.
	streams   net.Listener
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

// WithArtifacts resolves what a built resource currently runs. Optional: a
// resource whose source is an image or a component needs nothing here.
func WithArtifacts(a Artifacts) Option {
	return func(o *options) error {
		if a == nil {
			return errors.New("worker: a nil artifact resolver")
		}
		o.artifacts = a
		return nil
	}
}

// WithSecrets resolves a secret a run-time variable refers to. Optional: a
// resource with no secret variables needs nothing here.
func WithSecrets(s Secrets) Option {
	return func(o *options) error {
		if s == nil {
			return errors.New("worker: a nil secret resolver")
		}
		o.secrets = s
		return nil
	}
}

// WithServerOptions passes p2p/rpc.ServerOption values through to rpc.Listen.
func WithServerOptions(opts ...rpc.ServerOption) Option {
	return func(o *options) error {
		o.serverOptions = append(o.serverOptions, opts...)
		return nil
	}
}

// New validates and allocates. It binds nothing and starts nothing.
func New(h host.Host, cfg Config, opts ...Option) (*Service, error) {
	if h == nil {
		return nil, ErrNoHost
	}

	cfg, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}

	var o options
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, fmt.Errorf("worker: option: %w", err)
		}
	}
	if o.register == nil {
		return nil, ErrNoRegister
	}
	if o.pool == nil {
		return nil, ErrNoPool
	}
	cacheOptions := []projects.CacheOption{
		projects.WithTTL(cfg.PointerTTL),
		projects.WithMax(cfg.MaxProjects),
	}
	if o.now != nil {
		cacheOptions = append(cacheOptions, projects.WithClock(o.now))
	}

	projects, err := projects.NewCache(o.register, cacheOptions...)
	if err != nil {
		return nil, err
	}

	now := o.now
	if now == nil {
		now = time.Now
	}

	return &Service{
		host:          h,
		cfg:           cfg,
		register:      o.register,
		pool:          o.pool,
		artifacts:     o.artifacts,
		secrets:       o.secrets,
		meter:         o.meter,
		now:           now,
		projects:      projects,
		serverOptions: o.serverOptions,
	}, nil
}

// Invoke resolves a target and runs one request against it.
func (s *Service) Invoke(ctx context.Context, t Target, req *invoke.Request) (*invoke.Response, error) {
	// THE CLOCK STARTS AT THE RESOLVE, because that is when this cloud started
	// spending on the request: reading a pointer, fetching an object and
	// starting a cold instance are all work a caller caused, and a duration
	// that began after them would bill a cold start as if it were free.
	started := s.now()

	spec, err := s.Resolve(ctx, t)
	if err != nil {
		s.emit(ctx, accounted(t, "", 0, OpRefuse, s.now().Sub(started)))
		return nil, err
	}

	// AN MCP RUNS NO ENGINE. It is a protocol in front of the functions it
	// declares access to, so it is answered here rather than handed to a tier
	// that would ask for the output of a build that never happened.
	if t.Kind == mcpKind {
		res, err := s.serveMCP(ctx, t, spec, req)
		if err != nil {
			s.emit(ctx, accounted(t, spec.Revision, spec.Memory, OpRefuse, s.now().Sub(started)))
			return nil, err
		}
		s.emit(ctx, accounted(t, spec.Revision, spec.Memory, OpInvoke, s.now().Sub(started)))
		return res, nil
	}

	res, err := s.pool.Do(ctx, spec, req)
	if err != nil {
		s.emit(ctx, accounted(t, spec.Revision, spec.Memory, OpRefuse, s.now().Sub(started)))
		return nil, err
	}

	s.emit(ctx, accounted(t, spec.Revision, spec.Memory, OpInvoke, s.now().Sub(started)))
	return res, nil
}

// Transform runs one request through a transformer and answers with what it
// decided.
//
// IT IS METERED AS AN INVOCATION, because it is one: a transformer is a
// workload, it runs somebody's code, and it holds a pooled instance for the
// duration. A chain of three that ran unbilled would be a way to run code on
// this cloud for free, and the request that pays for the handler is not the same
// request as the three that shaped it.
//
// IT REFUSES A FUNCTION THAT IS NOT A TRANSFORMER, at the resolve, rather than
// letting the tier discover it: `implements` is on the spec, and a gateway that
// named an ordinary handler in its `transformers:` list would otherwise reach a
// component that reads a request as a request and answers with a response the
// chain reads as an outcome.
func (s *Service) Transform(ctx context.Context, t Target, req *invoke.Request) (*invoke.Outcome, error) {
	started := s.now()

	spec, err := s.Resolve(ctx, t)
	if err != nil {
		s.emit(ctx, accounted(t, "", 0, OpRefuse, s.now().Sub(started)))
		return nil, err
	}
	if world := spec.Settings[wasm.SettingImplements]; world != wasm.WorldGatewayTransformer {
		s.emit(ctx, accounted(t, spec.Revision, spec.Memory, OpRefuse, s.now().Sub(started)))
		return nil, fmt.Errorf("%w: %s implements %q rather than a transformer", ErrNoTarget, t.Name, world)
	}

	out, err := s.pool.Transform(ctx, spec, req)
	if err != nil {
		s.emit(ctx, accounted(t, spec.Revision, spec.Memory, OpRefuse, s.now().Sub(started)))
		return nil, err
	}

	s.emit(ctx, accounted(t, spec.Revision, spec.Memory, OpInvoke, s.now().Sub(started)))
	return out, nil
}

// Route asks a router where one request should go.
//
// METERED AND WORLD-CHECKED FOR THE REASONS Transform IS. A router runs
// somebody's code on a pooled instance, and a gateway that named an ordinary
// handler as its router would otherwise reach a component that answers with a
// response the gateway reads as a decision.
func (s *Service) Route(ctx context.Context, t Target, req *invoke.Request) (*invoke.Routing, error) {
	started := s.now()

	spec, err := s.Resolve(ctx, t)
	if err != nil {
		s.emit(ctx, accounted(t, "", 0, OpRefuse, s.now().Sub(started)))
		return nil, err
	}
	if world := spec.Settings[wasm.SettingImplements]; world != wasm.WorldGatewayRouter {
		s.emit(ctx, accounted(t, spec.Revision, spec.Memory, OpRefuse, s.now().Sub(started)))
		return nil, fmt.Errorf("%w: %s implements %q rather than a router", ErrNoTarget, t.Name, world)
	}

	out, err := s.pool.Route(ctx, spec, req)
	if err != nil {
		s.emit(ctx, accounted(t, spec.Revision, spec.Memory, OpRefuse, s.now().Sub(started)))
		return nil, err
	}

	s.emit(ctx, accounted(t, spec.Revision, spec.Memory, OpInvoke, s.now().Sub(started)))
	return out, nil
}

// Direct asks a PROXY ROUTER where one connection should go.
//
// METERED AND WORLD-CHECKED FOR THE REASONS Route IS, and asked once per
// decision rather than once per connection: a router that cannot tell yet is
// asked again with more bytes, and each of those is somebody's code running on
// a pooled instance.
func (s *Service) Direct(ctx context.Context, t Target, conn *invoke.Connection, peeked []byte) (*invoke.Direction, error) {
	started := s.now()

	spec, err := s.Resolve(ctx, t)
	if err != nil {
		s.emit(ctx, accounted(t, "", 0, OpRefuse, s.now().Sub(started)))
		return nil, err
	}
	if world := spec.Settings[wasm.SettingImplements]; world != wasm.WorldProxyRouter {
		s.emit(ctx, accounted(t, spec.Revision, spec.Memory, OpRefuse, s.now().Sub(started)))
		return nil, fmt.Errorf("%w: %s implements %q rather than a proxy router", ErrNoTarget, t.Name, world)
	}

	out, err := s.pool.Direct(ctx, spec, conn, peeked)
	if err != nil {
		s.emit(ctx, accounted(t, spec.Revision, spec.Memory, OpRefuse, s.now().Sub(started)))
		return nil, err
	}

	s.emit(ctx, accounted(t, spec.Revision, spec.Memory, OpInvoke, s.now().Sub(started)))
	return out, nil
}

// Shape runs one chunk of a connection through a PROXY TRANSFORMER.
//
// IT IS METERED PER CHUNK, which is the honest reading of what it costs: a
// transformer on a proxy runs somebody's code over every byte in both
// directions, and a conversation that ran unbilled would be a way to run code
// on this cloud for free for as long as a socket stays open.
func (s *Service) Shape(ctx context.Context, t Target, stream *invoke.Stream) (*invoke.Shaping, error) {
	started := s.now()

	spec, err := s.Resolve(ctx, t)
	if err != nil {
		s.emit(ctx, accounted(t, "", 0, OpRefuse, s.now().Sub(started)))
		return nil, err
	}
	if world := spec.Settings[wasm.SettingImplements]; world != wasm.WorldProxyTransformer {
		s.emit(ctx, accounted(t, spec.Revision, spec.Memory, OpRefuse, s.now().Sub(started)))
		return nil, fmt.Errorf("%w: %s implements %q rather than a proxy transformer", ErrNoTarget, t.Name, world)
	}

	out, err := s.pool.Shape(ctx, spec, stream)
	if err != nil {
		s.emit(ctx, accounted(t, spec.Revision, spec.Memory, OpRefuse, s.now().Sub(started)))
		return nil, err
	}

	s.emit(ctx, accounted(t, spec.Revision, spec.Memory, OpInvoke, s.now().Sub(started)))
	return out, nil
}

// Start listens and serves. Calling it twice, or after Close, is an error.
func (s *Service) Start(ctx context.Context) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()

	if s.closed.Load() {
		return errors.New("worker: closed")
	}
	if s.server != nil {
		return errors.New("worker: already started")
	}

	server, err := rpc.Listen(ctx, s.host, Protocol, s.mount(), s.serverOptions...)
	if err != nil {
		return fmt.Errorf("worker: listen on %s: %w", Protocol, err)
	}
	s.server = server

	return s.serveStreams(ctx)
}

// Close is idempotent and stops serving.
//
// IT DOES NOT CLOSE THE POOL. The pool is per-node and shared with everything
// else the node runs, and it was handed in rather than built here — a worker
// that closed it would stop every other consumer's warm instances.
func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		s.startMu.Lock()
		if s.server != nil {
			s.closeErr = s.server.Close()
		}
		if s.streams != nil {
			s.streams.Close()
		}
		s.startMu.Unlock()
	})
	return s.closeErr
}

// Loaded is how many compiled projects are held. It exists for a test and for
// an operator asking what a node is holding.
func (s *Service) Loaded() int { return s.projects.Held() }

// Serving is what this node can answer for cheaply, in the form a router reads:
// the service itself, plus one entry per environment whose compiled project it
// already holds.
//
// IT IS A HINT AND NOT A CLAIM OF EXCLUSIVITY. A worker that holds nothing can
// still run anything — it fetches the project first — so a router prefers a
// node that has it and falls back to any. Without the fallback, a project
// nobody has loaded yet would have nowhere to go.
//
// The list is SORTED because pkg/stats matches a name with a binary search.
func (s *Service) Serving() []string {
	out := []string{Descriptor.Name}
	for _, env := range s.projects.Environments() {
		out = append(out, Descriptor.Name+"/"+env)
	}
	sort.Strings(out)
	return out
}
