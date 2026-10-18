// Package auth is v2's rewrite of services/auth: the Project, Repository,
// Hook, Domain and Certificate RPCs declared in
// pkg/proto/tau/auth/v2/auth.proto, served to other tau nodes over
// p2p/rpc (ConnectRPC on a libp2p stream). See auth.proto's own package doc
// for what is deliberately not modelled here (the GitHub-token HTTP surface,
// ACME's health/stats boilerplate), and service-conventions.md for the shape
// every v2 service follows.
package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/libp2p/go-libp2p/core/host"

	"github.com/taubyte/tau/p2p/rpc"
)

// Service is auth's whole state: the host it answers on, the store it was
// handed, and the operator's config.
type Service struct {
	host host.Host
	kv   Store
	cfg  Config

	serverOptions []rpc.ServerOption

	// startMu guards server: Start and Close both read-then-write it, and
	// nothing about this type promises a caller will only ever touch one
	// Service from one goroutine.
	startMu   sync.Mutex
	server    *rpc.Server
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

// Option configures a Service at construction. v2's tunables are fields set
// through Option, never package-level vars every instance in a process
// would share (service-conventions.md §1's complaint about
// services/patrick/service.go's BootstrapTime and friends).
type Option func(*Service) error

// WithServerOptions passes p2p/rpc.ServerOption values through to
// rpc.Listen in Start — WithPrefaceTimeout, WithHTTPServer, and whatever
// p2p/rpc grows next — without this package needing an option of its own
// for each one.
func WithServerOptions(opts ...rpc.ServerOption) Option {
	return func(s *Service) error {
		s.serverOptions = append(s.serverOptions, opts...)
		return nil
	}
}

// New validates and allocates. It binds nothing, opens nothing and creates
// nothing — no listener until Start, and no database ever — so a Service can
// be constructed just to be inspected, and a node can construct every
// service before starting any of them (service-conventions.md §2a).
//
// Three parameters, not one bag (config.go): the host it answers on, the
// store it persists into, and what the operator declared. That is the whole
// dependency, and it is deliberately narrow enough that the answer to "can
// this be built against mocknet.GenPeer() and an in-memory map, with nothing
// else standing up?" is yes — which is the falsifiable form of the
// narrow-dependency rule, and a test in this package rather than a claim
// here.
//
// A nil host is an error rather than a node this service invents for itself.
// v1 has the same fallback copied across four services —
// `if srv.node = cfg.Node(); srv.node == nil { tauConfig.NewNode(...) }`
// (services/auth/service.go:36) — and a service that can invent its own
// libp2p identity can be started two ways, one of which is never how
// production starts it (service-conventions.md §2b).
func New(h host.Host, store Store, cfg Config, opts ...Option) (*Service, error) {
	if h == nil {
		return nil, errors.New("auth: no host to answer on")
	}
	if store == nil {
		return nil, errors.New("auth: no store")
	}
	if cfg.DomainValidation.Private == "" || cfg.DomainValidation.Public == "" {
		return nil, errors.New("auth: domain validation needs both halves of a key pair")
	}

	s := &Service{host: h, kv: store, cfg: cfg}

	for _, opt := range opts {
		if err := opt(s); err != nil {
			// Nothing to unwind: New owns no resource by the time an
			// option runs. The first pass had to close a database it had
			// already opened here, which is the shape injecting the store
			// removes rather than handles.
			return nil, fmt.Errorf("auth: option: %w", err)
		}
	}

	return s, nil
}

// Start listens and serves. Calling it twice, or after Close, is an error
// rather than a second listener leaking or a silent no-op.
func (s *Service) Start(ctx context.Context) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()

	if s.closed.Load() {
		return errors.New("auth: closed")
	}
	if s.server != nil {
		return errors.New("auth: already started")
	}

	server, err := rpc.Listen(ctx, s.host, Protocol, s.mount(), s.serverOptions...)
	if err != nil {
		return fmt.Errorf("auth: listen on %s: %w", Protocol, err)
	}
	s.server = server
	return nil
}

// Close is idempotent and stops the one thing this service started: serving.
// It does not close the store, and cannot — Store has no Close method
// (config.go), because the store was handed in by whatever composed the node
// and is shared with the services composed beside it.
func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		s.startMu.Lock()
		if s.server != nil {
			s.closeErr = s.server.Close()
		}
		s.startMu.Unlock()
	})
	return s.closeErr
}
