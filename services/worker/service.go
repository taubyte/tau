// Package worker is where code runs:
//
//	a request names a resource     (project, environment, scope, kind, name)
//	  -> the register says what that environment currently runs
//	    -> the compiled project says what that resource declares about itself
//	      -> pkg/invoke runs it on the tier the config selected
//
// Every runtime value comes from step three; memory, timeout, concurrency and
// environment are PerEnv. Pooling is pkg/invoke's. A worker cannot publish and
// cannot invent an artifact (see Artifacts in resolve.go).
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
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
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

	projects *register.Cache[*specs.Project]
	// resolutions is what targets resolve to under each deployment's current object.
	resolutions resolutions

	serverOptions []rpc.ServerOption

	startMu sync.Mutex
	server  *rpc.Server

	// streams is the raw half (stream.go), a second listener on the same host reaching
	// this service's pool and resolver.
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
	projects, err := register.NewCache(o.register, specs.Load, register.WithMax(cfg.MaxProjects))
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
	// The clock starts at the resolve, so a cold start is billed.
	started := s.now()

	spec, err := s.Resolve(ctx, t)
	if err != nil {
		s.emit(ctx, accounted(t, "", 0, OpRefuse, s.now().Sub(started)))
		return nil, err
	}

	// An mcp runs no engine and is answered here.
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

// Transform runs one request through a transformer and answers with what it decided.
// Metered as an invocation, since it runs somebody's code on a pooled instance.
// Refuses a function that is not a transformer at the resolve.
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

// Route asks a router where one request should go, metered and world-checked as
// Transform is.
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

// Direct asks a proxy router where one connection should go, metered and
// world-checked per decision.
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

// Shape runs one chunk of a connection through a proxy transformer, metered per
// chunk.
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

// Close is idempotent and stops serving. It does not close the pool, which is shared
// by the node and was handed in.
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

// Loaded is how many compiled projects are held.
func (s *Service) Loaded() int { return s.projects.Held() }

// Serving is what this node can answer for cheaply: the service, plus one entry per
// environment whose compiled project it holds. A hint, not exclusivity: any worker
// can fetch a project. Sorted, since pkg/stats binary-searches it.
func (s *Service) Serving() []string {
	out := []string{Descriptor.Name}
	for _, at := range s.projects.Deployments() {
		out = append(out, Descriptor.Name+"/"+at.String())
	}
	sort.Strings(out)
	return out
}
