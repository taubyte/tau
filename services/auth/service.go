// Package auth is v2's authorization and identity service: the one RPC
// declared in pkg/proto/tau/auth/v2/auth.proto, served to other tau nodes over
// p2p/rpc (ConnectRPC on a libp2p stream).
//
// It answers whether someone a forge vouched for is admitted to this cloud.
// Projects and repositories are the register's (services/register), a
// project's webhook secret and deploy keys are the secrets service's, and
// whether a caller may write to a repository is the forge's answer, asked by
// the build frontend. auth.proto's package doc says why each lives where it
// does.
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

// Service is auth's whole state: the host it answers on and the identity it
// answers from. It holds no store — nothing it answers is a row it keeps.
type Service struct {
	host host.Host

	identity Identity

	serverOptions []rpc.ServerOption

	startMu   sync.Mutex
	server    *rpc.Server
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

// Option configures a Service at construction.
type Option func(*Service) error

// WithServerOptions passes p2p/rpc.ServerOption values through to rpc.Listen.
func WithServerOptions(opts ...rpc.ServerOption) Option {
	return func(s *Service) error {
		s.serverOptions = append(s.serverOptions, opts...)
		return nil
	}
}

// New validates and allocates. It binds nothing until Start.
func New(h host.Host, opts ...Option) (*Service, error) {
	if h == nil {
		return nil, errors.New("auth: no host to answer on")
	}

	s := &Service{host: h, identity: AdmitAll{}}
	for _, opt := range opts {
		if err := opt(s); err != nil {
			return nil, fmt.Errorf("auth: option: %w", err)
		}
	}
	return s, nil
}

// Start listens and serves. Calling it twice, or after Close, is an error.
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

// Close is idempotent and stops serving.
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
