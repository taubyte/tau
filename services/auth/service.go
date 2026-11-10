// Package auth is v2's identity service, served to other tau nodes over p2p/rpc.
// It keeps this cloud's users (the configured admin, local users, and people signed
// in through a forge or OIDC provider), issues and verifies their tau tokens,
// brokers sign-in through the cloud's own OAuth apps, answers whether someone a
// forge vouched for is admitted, and keeps the project table (projects). auth.proto's
// package doc says what lives elsewhere.
package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	"github.com/taubyte/tau/services/auth/projects"
)

var serviceLog = logging.Logger("tau.auth.service")

func (s *Service) logs() *logging.ZapEventLogger {
	if s.log != nil {
		return s.log
	}
	return serviceLog
}

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

	// projects is the project table, kept in the users' store.
	projects *projects.Table

	oidcMu    sync.Mutex
	providers map[string]*oidc.Provider

	serverOptions []rpc.ServerOption

	// vaultClient is the vault Start reached, closed with the service.
	vaultClient io.Closer

	startMu   sync.Mutex
	server    *rpc.Server
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool

	// pollFor bounds one DevicePoll, and log is where the service writes: the
	// package's when unset.
	pollFor time.Duration
	log     *logging.ZapEventLogger
}

type Option func(*Service) error

func withServerOptions(opts ...rpc.ServerOption) Option {
	return func(s *Service) error {
		s.serverOptions = append(s.serverOptions, opts...)
		return nil
	}
}

// withUsers gives auth its users: the store they are kept in, the vault its
// keys are, and the configuration that names the admin and the sign-in
// providers. Without it auth answers admission only, admitting whoever a forge
// vouched for.
func withUsers(db kvdb.Instance, v Vault, cfg Config) Option {
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

func withForges(r *forge.Registry) Option {
	return func(s *Service) error {
		if r == nil {
			return errors.New("auth: a nil forge registry")
		}
		s.forges = r
		return nil
	}
}

// withHTTPClient is what token endpoints and OIDC discovery are reached
// with. Redirects are never followed whatever it says.
func withHTTPClient(c *http.Client) Option {
	return func(s *Service) error {
		if c == nil {
			return errors.New("auth: a nil http client")
		}
		s.http = c
		return nil
	}
}

func withClock(now func() time.Time) Option {
	return func(s *Service) error {
		if now == nil {
			return errors.New("auth: a nil clock")
		}
		s.now = now
		return nil
	}
}

// New is auth, unstarted: Start builds it from its node.
func New() *Service { return &Service{} }

// build is auth over what it is given, not yet serving.
func build(h host.Host, opts ...Option) (*Service, error) {
	s := &Service{}
	if err := s.assemble(h, opts...); err != nil {
		return nil, err
	}
	return s, nil
}

// assemble validates and allocates. It binds nothing until serve.
func (s *Service) assemble(h host.Host, opts ...Option) error {
	if h == nil {
		return errors.New("auth: no host to answer on")
	}

	s.host, s.now, s.providers, s.hashing = h, time.Now, map[string]*oidc.Provider{}, make(chan struct{}, 2)
	for _, opt := range opts {
		if err := opt(s); err != nil {
			return fmt.Errorf("auth: option: %w", err)
		}
	}
	s.http = oidc.Client(s.http)
	if s.store != nil {
		s.projects = projects.New(projects.Over(s.store.db), s.now)
		s.keys = newKeyring(s.vault, *s.store, h.ID().String(), func() int64 { return s.now().UnixNano() })
		for _, f := range s.cfg.Forges {
			if s.forges == nil {
				return errors.New("auth: forge sign-in is configured with no forge registry")
			}
			if _, err := s.forges.Driver(f.Host); err != nil {
				return fmt.Errorf("auth: forge app for %s: %w", f.Host, err)
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
	return nil
}

// Projects is the project table auth keeps, nil when it keeps no store.
func (s *Service) Projects() projects.Projects {
	if s.projects == nil {
		return nil
	}
	return s.projects
}

// serve listens and serves. Calling it twice, or after Close, is an error.
func (s *Service) serve(ctx context.Context) error {
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
		if s.vaultClient != nil {
			s.vaultClient.Close()
		}
	})
	return s.closeErr
}
