// Package secrets serves the values a config deliberately does not carry:
// four RPCs, declared in pkg/proto/tau/secrets/v1/secrets.proto.
//
// # Why this is not a table in the register
//
// Every other v2 store is written by a push. The register's only writer is the
// build service, because a deploy that can happen from a laptop bypasses git,
// its authorization and its record. A secret inverts that completely — putting
// the value in git IS the failure — so it is written out of band by whoever
// holds it, and a rotation is one call with no commit and no build behind it.
// The vocabulary says as much where the `secret` kind is declared: the value
// "lives outside git in the secrets service, keyed <project>/<environment>/<key>,
// so rotation needs no push".
//
// Two stores with two writers, two lifecycles and two trust properties are two
// services. A `secrets` column on a compiled object would have made a rotation
// a redeploy.
//
// # What an operator declares about a secrets node
//
// Nothing, which is why this package has no Config. Its whole behaviour comes
// from the store it is given and the requests it receives, and a Config struct
// with no fields in it would be a slot inviting one.
//
// # The thing this package does not do
//
// AUTHORIZATION. Any peer that can reach this service can read every value in
// it. That is v2's OSS trust model stated plainly — swarm membership is the
// boundary, the private swarm key enforces it — and it is why a deployment
// that does not trust its own nodes needs a different Store and a caller
// identity on every request, not a flag here. See .notes/tee/v2-seam.md.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/libp2p/go-libp2p/core/host"

	"github.com/taubyte/tau/p2p/rpc"
)

// Service is the vault plus the host it answers on.
type Service struct {
	host  host.Host
	vault *Vault

	serverOptions []rpc.ServerOption

	startMu   sync.Mutex
	server    *rpc.Server
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

// Option injects what this service is built from.
type Option func(*options) error

type options struct {
	store         Store
	serverOptions []rpc.ServerOption
}

// WithStore is where values live. Required.
//
// It takes a STORE rather than a built *Vault because the vault is a pure
// value over a store and there is nothing for a composer to decide about it.
// What a composer does decide is where the rows go, and that is this argument.
func WithStore(s Store) Option {
	return func(o *options) error {
		if s == nil {
			return ErrNoStore
		}
		o.store = s
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

var ErrNoHost = errors.New("secrets: no host to answer on")

// New validates and allocates. It binds nothing and opens nothing — no
// listener until Start — so a node can construct every service before starting
// any of them.
func New(h host.Host, opts ...Option) (*Service, error) {
	if h == nil {
		return nil, ErrNoHost
	}

	var o options
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, fmt.Errorf("secrets: option: %w", err)
		}
	}
	if o.store == nil {
		return nil, ErrNoStore
	}

	vault, err := NewVault(o.store)
	if err != nil {
		// Unreachable while o.store is checked above. Kept rather than dropped
		// so a change to NewVault's rules cannot pass silently through here.
		return nil, err
	}

	return &Service{host: h, vault: vault, serverOptions: o.serverOptions}, nil
}

// Start listens and serves. Calling it twice, or after Close, is an error
// rather than a second listener leaking or a silent no-op.
func (s *Service) Start(ctx context.Context) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()

	if s.closed.Load() {
		return errors.New("secrets: closed")
	}
	if s.server != nil {
		return errors.New("secrets: already started")
	}

	server, err := rpc.Listen(ctx, s.host, Protocol, s.mount(), s.serverOptions...)
	if err != nil {
		return fmt.Errorf("secrets: listen on %s: %w", Protocol, err)
	}
	s.server = server
	return nil
}

// Close is idempotent and stops the one thing this service started: serving.
// It does not close the store, which was handed in by whatever composed the
// node and is shared with the services composed beside it.
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

// Local is the vault this service answers from, for a composer on the same
// node. It satisfies services/worker's Secrets interface directly, so a node
// running both a worker and a secrets service resolves a secret without a
// stream — and one running only a worker uses a Client, with the same method.
func (s *Service) Local() *Vault { return s.vault }
