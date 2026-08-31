// Package auth is v2's rewrite of services/auth: the Project, Repository,
// Hook, Domain and Certificate RPCs declared in
// pkg/v2/proto/auth/v1/auth.proto, served to other tau nodes over
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

	"github.com/ipfs/go-log/v2"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/rpc"
)

var logger = log.Logger("tau.v2.auth")

// storeName is what this service's database is called on the shared
// kvdb.Factory — services/common/names.go's Auth ("auth") carried forward,
// since it names nothing v1-specific, only this service's own row on a
// factory every service on the node shares.
const storeName = "auth"

// Service is auth's whole state: the store and the two secrets
// (Register/Unregister's authorization and DomainService's signing key) it
// was constructed with.
type Service struct {
	node          peer.Node
	kv            kvdb.KVDB
	dvPrivateKey  []byte
	dvPublicKey   []byte
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

// New validates, allocates and mounts. It binds nothing — no listener opens
// until Start — so a Service can be constructed just to be inspected, and a
// node can construct every service before starting any of them
// (service-conventions.md §2a).
func New(ctx context.Context, cfg Config, opts ...Option) (*Service, error) {
	node := cfg.Node()
	if node == nil {
		// v1 falls back to tauConfig.NewNode(...) here on every one of its
		// four services (services/auth/service.go:36, and identically in
		// tns, patrick, gateway) — a service that can invent its own libp2p
		// identity can be started two ways, and one of them is never the
		// way production starts it (service-conventions.md §2b). v2 does
		// not carry that fallback forward: a nil Node is an error.
		return nil, errors.New("auth: config has no node")
	}

	privateKey, publicKey := cfg.DomainValidation()
	if len(privateKey) == 0 || len(publicKey) == 0 {
		return nil, errors.New("auth: domain validation keys cannot be empty")
	}

	factory := cfg.Databases()
	if factory == nil {
		return nil, errors.New("auth: config has no database factory")
	}

	rebroadcastIntervalSec := 5
	if cfg.DevMode() {
		rebroadcastIntervalSec = 1
	}

	kv, err := factory.New(logger, storeName, rebroadcastIntervalSec)
	if err != nil {
		return nil, fmt.Errorf("auth: opening store: %w", err)
	}

	s := &Service{
		node:         node,
		kv:           kv,
		dvPrivateKey: privateKey,
		dvPublicKey:  publicKey,
	}

	for _, opt := range opts {
		if err := opt(s); err != nil {
			kv.Close()
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

	server, err := rpc.Listen(ctx, s.node.Peer(), Protocol, s.mount(), s.serverOptions...)
	if err != nil {
		return fmt.Errorf("auth: listen on %s: %w", Protocol, err)
	}
	s.server = server
	return nil
}

// Close is idempotent and reverses construction order: stop serving, then
// close the store. It never closes the kvdb.Factory Config handed New — the
// factory is shared with every other service on the node, and this service
// only ever owned the one database it opened from it
// (service-conventions.md §2e: "anything the service constructs, the
// service closes; anything it was handed, it does not").
func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		s.startMu.Lock()
		if s.server != nil {
			s.closeErr = s.server.Close()
		}
		s.startMu.Unlock()
		s.kv.Close()
	})
	return s.closeErr
}
