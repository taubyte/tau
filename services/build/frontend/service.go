package frontend

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/taubyte/tau/pkg/tenancy"
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
	hooks      Hooks
	deliveries Deliveries
	projects   Projects
	callers    Callers
	backend    Backend
	vault      Vault
	linker     Linker
	assets     Assets
	tenancy    tenancy.Tenancy
	streams    Streams
	linked     Linked
	authAPI    AuthAPI
	cfg        Config
	cloudFqdn  string

	now    func() time.Time
	logf   func(string, ...any)
	tune   func(*http.Server)
	tls    *tls.Config
	router http.Handler

	// FOUR LIMITERS, TWO PER PUBLIC SURFACE, and one cache of auth's answers.
	// The keyed ones are all keyed by a string a stranger chooses, so all three
	// are the same bounded, expiring map; the global ones are one bucket each
	// and cannot be keyed at all. See secrets.go and ratelimit.go.
	secrets       *hookCache
	hookBuckets   *cache[*bucket]
	callerBuckets *cache[*bucket]
	globalHooks   *bucket
	globalJobs    *bucket

	startMu     sync.Mutex
	stopStreams context.CancelFunc
	streaming   sync.WaitGroup
	server      *http.Server
	listener    net.Listener
	serving     chan struct{}

	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

// Option configures a Service at construction. v2's tunables are fields set
// through Option, never package-level vars every instance in a process shares.
//
// IT CONFIGURES AN options VALUE, NOT THE SERVICE. A Service is built once, from
// a validated snapshot, so there is no window in which a half-configured one
// exists and no second way to make one.
type Option func(*options) error

type options struct {
	hooks      Hooks
	deliveries Deliveries
	projects   Projects
	callers    Callers
	backend    Backend
	vault      Vault
	linker     Linker
	assets     Assets
	tenancy    tenancy.Tenancy
	streams    Streams
	linked     Linked
	authAPI    AuthAPI
	cloudFqdn  string

	tune func(*http.Server)
	tls  *tls.Config
	now  func() time.Time
	logf func(format string, args ...any)
}

// WithHooks is where a webhook's secret is verified against. Required.
func WithHooks(h Hooks) Option {
	return func(o *options) error {
		if h == nil {
			return errors.New("build: no hooks to verify a webhook against")
		}
		o.hooks = h
		return nil
	}
}

// WithProjects is what a caller is authorized against. Required.
func WithProjects(p Projects) Option {
	return func(o *options) error {
		if p == nil {
			return errors.New("build: no projects to authorize against")
		}
		o.projects = p
		return nil
	}
}

// WithBackend is where work is queued. Required.
//
// IT IS AN INTERFACE THIS PACKAGE DECLARES, never a client this package builds:
// a public role that could construct its own route to the backend could be
// started a way production never starts it.
func WithBackend(b Backend) Option {
	return func(o *options) error {
		if b == nil {
			return errors.New("build: no backend to queue into")
		}
		o.backend = b
		return nil
	}
}

// WithVault is where a project's secret values are written. Optional: a
// frontend without one refuses the secret routes the way it refuses a project
// the caller may not touch, so a cloud with no vault is not an oracle for
// whether it has one.
func WithVault(v Vault) Option {
	return func(o *options) error {
		if v == nil {
			return errors.New("build: no vault to write secrets into")
		}
		o.vault = v
		return nil
	}
}

// WithLinker is where a linked project and its grants are recorded — the
// register, which this public role may only forward to after authorizing the
// caller (projectapi.go). Optional: without one the project routes answer the
// 404 a denial gets.
func WithLinker(l Linker) Option {
	return func(o *options) error {
		if l == nil {
			return errors.New("build: no linker to record projects with")
		}
		o.linker = l
		return nil
	}
}

// WithAssets is where a finished job's logs and artifacts are read from.
// Optional: without it those routes answer 503.
func WithAssets(a Assets) Option {
	return func(o *options) error {
		if a == nil {
			return errors.New("build: nil assets")
		}
		o.assets = a
		return nil
	}
}

// WithDeliveries is how a webhook delivery is verified and read. Required.
func WithDeliveries(d Deliveries) Option {
	return func(o *options) error {
		if d == nil {
			return errors.New("build: no way to verify a webhook delivery")
		}
		o.deliveries = d
		return nil
	}
}

// WithCallers verifies a caller's tau token. Required.
func WithCallers(c Callers) Option {
	return func(o *options) error {
		if c == nil {
			return errors.New("build: no way to verify a caller")
		}
		o.callers = c
		return nil
	}
}

// WithTenancy limits the repositories this cloud links and builds to the
// namespaces t serves. Without it, every repository.
func WithTenancy(t tenancy.Tenancy) Option {
	return func(o *options) error {
		o.tenancy = t
		return nil
	}
}

// WithStreams subscribes this frontend to every forge event stream s offers,
// with linked as the repositories whose heads a reconnect compares. Optional:
// without it only webhooks trigger builds. See streams.go.
func WithStreams(s Streams, linked Linked) Option {
	return func(o *options) error {
		if s == nil || linked == nil {
			return errors.New("build: event streams need both the streams and the linked repositories")
		}
		o.streams, o.linked = s, linked
		return nil
	}
}

// WithCloudFqdn is the cloud's zone, which GET /cloud answers with.
func WithCloudFqdn(fqdn string) Option {
	return func(o *options) error {
		o.cloudFqdn = strings.ToLower(strings.Trim(fqdn, "."))
		return nil
	}
}

// WithHTTPServer hands the caller the *http.Server before it serves, for the
// knobs this package does not name — TLS, connection state hooks, a custom
// ErrorLog. It mirrors p2p/rpc's own WithHTTPServer so the two roles are tuned
// the same way.
//
// Handler, ReadHeaderTimeout and IdleTimeout are set before it runs, so a
// caller can read them; overwriting Handler is how you make this service serve
// something other than its own routes, and nothing stops you.
func WithHTTPServer(f func(*http.Server)) Option {
	return func(o *options) error {
		if f == nil {
			return errors.New("build: http server hook must not be nil")
		}
		o.tune = f
		return nil
	}
}

// WithTLS serves Listen over TLS, answering each handshake from c. A node
// hands over the cloud's certificates for `build-frontend.<cloud fqdn>`,
// which is the address a forge's webhook is installed at.
func WithTLS(c *tls.Config) Option {
	return func(o *options) error {
		if c == nil {
			return errors.New("build: a nil tls config")
		}
		o.tls = c
		return nil
	}
}

// WithClock replaces the source of time for the secret cache and both rate
// limiters. It is what lets a test watch an entry expire or a bucket refill
// without sleeping — which is most of why those two are testable at all.
func WithClock(now func() time.Time) Option {
	return func(o *options) error {
		if now == nil {
			return errors.New("build: clock must not be nil")
		}
		o.now = now
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
	return func(o *options) error {
		if logf == nil {
			return errors.New("build: logger must not be nil")
		}
		o.logf = logf
		return nil
	}
}

// New validates and allocates. It binds nothing — no listener until Start — so
// a Service can be constructed to be inspected, and a node can construct every
// service before starting any of them (service-conventions.md §2a). v1's New
// serves: v1's services/patrick/service.go starts its stream, then can still fail
// and leak it.
//
// Five required dependencies, each an interface this package declares rather than a
// client it builds, and each injected with a With… . A nil one is an error,
// never something this service invents.
func New(cfg Config, opts ...Option) (*Service, error) {
	effective, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}

	o := options{now: time.Now, logf: func(string, ...any) {}}
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			// Nothing to unwind: New owns no resource by the time an option
			// runs.
			return nil, fmt.Errorf("build: option: %w", err)
		}
	}

	// Every dependency is checked AFTER the options have run, so a missing one
	// is reported as missing rather than as a nil that surfaces on the first
	// request.
	switch {
	case o.hooks == nil:
		return nil, errors.New("build: no hooks to verify a webhook against")
	case o.deliveries == nil:
		return nil, errors.New("build: no way to verify a webhook delivery")
	case o.projects == nil:
		return nil, errors.New("build: no projects to authorize against")
	case o.backend == nil:
		return nil, errors.New("build: no backend to queue into")
	case o.callers == nil:
		return nil, errors.New("build: no way to verify a caller")
	}

	s := &Service{
		hooks:      o.hooks,
		deliveries: o.deliveries,
		projects:   o.projects,
		callers:    o.callers,
		backend:    o.backend,
		vault:      o.vault,
		linker:     o.linker,
		assets:     o.assets,
		tenancy:    o.tenancy,
		streams:    o.streams,
		linked:     o.linked,
		authAPI:    o.authAPI,
		cfg:        effective,
		cloudFqdn:  o.cloudFqdn,
		tune:       o.tune,
		tls:        o.tls,
		now:        o.now,
		logf:       o.logf,
	}

	// ONLY A USABLE HOOK IS REMEMBERED — never a miss, never a failure to ask;
	// see secrets.go.
	s.secrets = newHookCache(s.cfg.SecretCacheMax, s.cfg.SecretCacheTTL, s.now, s.fetchHook)
	// A bucket's lifetime is one refill period, because a bucket untouched for
	// that long has refilled to full and is indistinguishable from a new one.
	//
	// THE TWO KEYED MAPS ARE SEPARATE. One map shared between hook ids and
	// credentials would let a flood of invented keys on either surface evict
	// the other's buckets — and an evicted bucket comes back full, which is the
	// per-key limiter being turned off by a caller rather than by an operator.
	s.hookBuckets = newCache[*bucket](s.cfg.SecretCacheMax, max(s.cfg.HookRate.Per, time.Second), s.now)
	s.callerBuckets = newCache[*bucket](s.cfg.SecretCacheMax, max(s.cfg.CallerRate.Per, time.Second), s.now)
	s.globalHooks = newBucket(s.cfg.WebhookRate, s.now)
	s.globalJobs = newBucket(s.cfg.JobRate, s.now)
	s.router = s.cors(s.mount())

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
	served := listener
	if s.tls != nil {
		served = tls.NewListener(listener, s.tls)
	}
	s.serving = make(chan struct{})

	go func() {
		defer close(s.serving)
		if err := server.Serve(served); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log("serving on %s: %v", listener.Addr(), err)
		}
	}()

	s.startStreams(ctx)
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
		server, serving, stop := s.server, s.serving, s.stopStreams
		s.startMu.Unlock()

		if stop != nil {
			stop()
			s.streaming.Wait()
		}

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
