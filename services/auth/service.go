// Package auth is v2's identity service, served to other tau nodes over p2p/rpc.
// It keeps this cloud's users (the configured admin, local users, and people signed
// in through a forge or OIDC provider), issues and verifies their tau tokens,
// brokers sign-in through the cloud's own OAuth apps, and answers whether someone a
// forge vouched for is admitted. auth.proto's package doc says what lives elsewhere.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	logging "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/host"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/services/auth/oidc"
)

var serviceLog = logging.Logger("tau.auth.service")

type Service struct {
	host host.Host

	identity Identity

	// The users half. Without a store a Service answers admission only.
	cfg    Config
	store  *store
	keys   *keyring
	forges *forge.Registry
	vault  Vault
	http   *http.Client
	now    func() time.Time
	// hashing bounds concurrent Argon2 work: each is 64 MiB.
	hashing chan struct{}

	oidcMu    sync.Mutex
	providers map[string]*oidc.Provider

	serverOptions []rpc.ServerOption

	startMu   sync.Mutex
	server    *rpc.Server
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

type Option func(*Service) error

func WithServerOptions(opts ...rpc.ServerOption) Option {
	return func(s *Service) error {
		s.serverOptions = append(s.serverOptions, opts...)
		return nil
	}
}

// WithUsers gives auth its users: the store they are kept in, the vault its
// keys are, and the configuration that names the admin and the sign-in
// providers. Without it auth answers admission only, admitting whoever a forge
// vouched for.
func WithUsers(db kvdb.KVDB, v Vault, cfg Config) Option {
	return func(s *Service) error {
		if db == nil || v == nil {
			return errors.New("auth: users need a store and a vault")
		}
		if err := cfg.defaults(); err != nil {
			return err
		}
		s.store, s.vault, s.cfg = &store{db: db}, v, cfg
		return nil
	}
}

func WithForges(r *forge.Registry) Option {
	return func(s *Service) error {
		if r == nil {
			return errors.New("auth: a nil forge registry")
		}
		s.forges = r
		return nil
	}
}

// WithHTTPClient is what token endpoints and OIDC discovery are reached
// with. Redirects are never followed whatever it says.
func WithHTTPClient(c *http.Client) Option {
	return func(s *Service) error {
		if c == nil {
			return errors.New("auth: a nil http client")
		}
		s.http = c
		return nil
	}
}

func WithClock(now func() time.Time) Option {
	return func(s *Service) error {
		if now == nil {
			return errors.New("auth: a nil clock")
		}
		s.now = now
		return nil
	}
}

// New validates and allocates. It binds nothing until Start.
func New(h host.Host, opts ...Option) (*Service, error) {
	if h == nil {
		return nil, errors.New("auth: no host to answer on")
	}

	s := &Service{host: h, now: time.Now, providers: map[string]*oidc.Provider{}, hashing: make(chan struct{}, 2)}
	for _, opt := range opts {
		if err := opt(s); err != nil {
			return nil, fmt.Errorf("auth: option: %w", err)
		}
	}
	s.http = oidc.Client(s.http)
	if s.store != nil {
		s.keys = newKeyring(s.vault, *s.store, h.ID().String(), func() int64 { return s.now().UnixNano() })
		for _, f := range s.cfg.Forges {
			if s.forges == nil {
				return nil, errors.New("auth: forge sign-in is configured with no forge registry")
			}
			if _, err := s.forges.Driver(f.Host); err != nil {
				return nil, fmt.Errorf("auth: forge app for %s: %w", f.Host, err)
			}
		}
	}
	if s.identity == nil {
		if s.store != nil {
			s.identity = &Users{s: s}
		} else {
			s.identity = AdmitAll{}
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
