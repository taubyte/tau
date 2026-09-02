package frontend

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	authv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/auth/v1"
)

// Service is build's public half: an http.Server, a mux, and the four
// questions it may ask the rest of this cloud.
//
// IT HOLDS NO host.Host, and the absence is the trust boundary as a field list.
// build.proto's header is explicit that this role serves no RPC of its own
// ("an empty surface is a strong claim and it is meant as one"), so this
// process has nothing to listen on a libp2p protocol with and cannot be dialled
// by the swarm. It reaches the backend and auth through interfaces a
// composition root supplies — services/build/clients holds the pool that
// satisfies them — which is also what lets every route in this package be
// tested against fakes with no host, no listener and no port.
type Service struct {
	hooks    Hooks
	projects Projects
	backend  Backend
	identity Identity
	cfg      Config

	now    func() time.Time
	logf   func(string, ...any)
	tune   func(*http.Server)
	router http.Handler

	// secrets and buckets are both keyed by a string a stranger chooses, so
	// both are the same bounded, expiring map. See secrets.go and ratelimit.go.
	secrets *cache[*authv1.GithubHook]
	buckets *cache[*bucket]
	global  *bucket

	startMu  sync.Mutex
	server   *http.Server
	listener net.Listener
	serving  chan struct{}

	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

// Option configures a Service at construction. v2's tunables are fields set
// through Option, never package-level vars every instance in a process shares.
type Option func(*Service) error

// WithHTTPServer hands the caller the *http.Server before it serves, for the
// knobs this package does not name — TLS, connection state hooks, a custom
// ErrorLog. It mirrors p2p/rpc's own WithHTTPServer so the two roles are tuned
// the same way.
//
// Handler, ReadHeaderTimeout and IdleTimeout are set before it runs, so a
// caller can read them; overwriting Handler is how you make this service serve
// something other than its own routes, and nothing stops you.
func WithHTTPServer(f func(*http.Server)) Option {
	return func(s *Service) error {
		if f == nil {
			return errors.New("build: http server hook must not be nil")
		}
		s.tune = f
		return nil
	}
}

// WithClock replaces the source of time for the secret cache and both rate
// limiters. It is what lets a test watch an entry expire or a bucket refill
// without sleeping — which is most of why those two are testable at all.
func WithClock(now func() time.Time) Option {
	return func(s *Service) error {
		if now == nil {
			return errors.New("build: clock must not be nil")
		}
		s.now = now
		return nil
	}
}

// WithLogger replaces where this service says what it refused and why.
//
// THE LOG IS PART OF THE DESIGN HERE, not decoration: almost every check in
// webhook.go answers a stranger with a status and nothing else, on purpose, so
// the log is the only place the reason exists. A hook whose secret is empty and
// a signature made under the wrong hook are both invisible without it.
func WithLogger(logf func(format string, args ...any)) Option {
	return func(s *Service) error {
		if logf == nil {
			return errors.New("build: logger must not be nil")
		}
		s.logf = logf
		return nil
	}
}

// New validates and allocates. It binds nothing — no listener until Start — so
// a Service can be constructed to be inspected, and a node can construct every
// service before starting any of them (service-conventions.md §2a). v1's New
// serves: services/patrick/service.go starts its stream, then can still fail
// and leak it.
//
// Four dependencies, and every one of them is an interface this package
// declares rather than a client it builds. A nil one is an error, never
// something this service invents: a public role that could construct its own
// route to the backend could be started a way production never starts it.
func New(hooks Hooks, projects Projects, backend Backend, identity Identity, cfg Config, opts ...Option) (*Service, error) {
	if hooks == nil {
		return nil, errors.New("build: no hooks to verify a webhook against")
	}
	if projects == nil {
		return nil, errors.New("build: no projects to authorize against")
	}
	if backend == nil {
		return nil, errors.New("build: no backend to queue into")
	}
	if identity == nil {
		return nil, errors.New("build: no identity to resolve a caller with")
	}

	effective, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}

	s := &Service{
		hooks:    hooks,
		projects: projects,
		backend:  backend,
		identity: identity,
		cfg:      effective,
		now:      time.Now,
		logf:     func(string, ...any) {},
	}

	for _, opt := range opts {
		if err := opt(s); err != nil {
			// Nothing to unwind: New owns no resource by the time an option
			// runs.
			return nil, fmt.Errorf("build: option: %w", err)
		}
	}

	// A miss is remembered only when it is an ANSWER — see secrets.go's
	// terminal. Remembering "auth is unreachable" would keep this node
	// refusing real pushes for a whole TTL after auth came back.
	s.secrets = newCache[*authv1.GithubHook](s.cfg.SecretCacheMax, s.cfg.SecretCacheTTL, s.now, terminal)
	// A bucket's lifetime is one refill period, because a bucket untouched for
	// that long has refilled to full and is indistinguishable from a new one.
	s.buckets = newCache[*bucket](s.cfg.SecretCacheMax, max(s.cfg.HookRate.Per, time.Second), s.now, nil)
	s.global = newBucket(s.cfg.WebhookRate, s.now)
	s.router = s.mount()

	return s, nil
}

// Handler is the routes, with no listener under them. It is exported because
// it is how this package is tested — every route runs on httptest.NewServer or
// on a bare ResponseRecorder, with no port bound anywhere — and because a node
// that terminates TLS elsewhere may want to mount these itself.
func (s *Service) Handler() http.Handler { return s.router }

// Start binds cfg.Listen and serves. AN http.Server ONLY: there is no
// rpc.Listen in this package and there is a test that reads the source to say
// so, because the day this role can be dialled by the swarm is the day every
// node in it believes a process whose input is an unauthenticated POST body.
//
// Calling it twice, or after Close, is an error rather than a second listener
// leaking or a silent no-op.
func (s *Service) Start(ctx context.Context) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()

	if s.closed.Load() {
		return errors.New("build: closed")
	}
	if s.server != nil {
		return errors.New("build: already started")
	}
	if s.cfg.Listen == "" {
		return errors.New("build: no address to listen on")
	}

	listener, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("build: listen on %s: %w", s.cfg.Listen, err)
	}

	server := &http.Server{
		Handler: s.router,
		// The one net/http default that is missing and matters: without it a
		// connection that never finishes its headers holds a goroutine for as
		// long as it likes.
		ReadHeaderTimeout: s.cfg.ReadHeaderTimeout,
		IdleTimeout:       s.cfg.IdleTimeout,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	if s.tune != nil {
		s.tune(server)
	}

	s.server = server
	s.listener = listener
	s.serving = make(chan struct{})

	go func() {
		defer close(s.serving)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log("serving on %s: %v", listener.Addr(), err)
		}
	}()

	return nil
}

// Addr is what Start actually bound, which is not what Config said when the
// port was zero. Nil before Start.
func (s *Service) Addr() net.Addr {
	s.startMu.Lock()
	defer s.startMu.Unlock()

	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// Close is idempotent and stops the one thing this service started. It gives
// in-flight requests ShutdownTimeout to finish and then takes the listener
// away regardless — a public listener that a slow client can keep open through
// shutdown is a public listener that does not shut down.
//
// It closes nothing it was handed: the pool behind Backend, Hooks and Projects
// belongs to whatever composed the node and is shared with the services beside
// it (service-conventions.md §2e).
func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)

		s.startMu.Lock()
		server, serving := s.server, s.serving
		s.startMu.Unlock()

		if server == nil {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
		defer cancel()

		s.closeErr = server.Shutdown(ctx)
		if s.closeErr != nil {
			s.closeErr = errors.Join(s.closeErr, server.Close())
		}
		<-serving
	})
	return s.closeErr
}

func (s *Service) log(format string, args ...any) { s.logf(format, args...) }
