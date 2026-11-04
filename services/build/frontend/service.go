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
	"github.com/taubyte/tau/services/build/uploads"
)

// Service is build's public half: an http.Server, a mux, and the questions it may
// ask the rest of the cloud through interfaces. It holds no host.Host, so the
// swarm cannot dial it.
type Service struct {
	hooks      Hooks
	deliveries Deliveries
	projects   Projects
	callers    Callers
	backend    Backend
	vault      Vault
	domains    Domains
	linker     Linker
	assets     Assets
	tenancy    tenancy.Tenancy
	streams    Streams
	linked     Linked
	authAPI    AuthAPI
	uploads    *uploads.Dir
	published  Published
	cfg        Config
	cloudFqdn  string

	now    func() time.Time
	logf   func(string, ...any)
	tune   func(*http.Server)
	tls    *tls.Config
	router http.Handler

	// Four limiters, two per public surface, and the hook cache; the keyed ones share
	// the bounded expiring map type (secrets.go, ratelimit.go).
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

// Option configures an options value; New builds the Service once from the
// validated snapshot.
type Option func(*options) error

type options struct {
	hooks      Hooks
	deliveries Deliveries
	projects   Projects
	callers    Callers
	backend    Backend
	vault      Vault
	domains    Domains
	linker     Linker
	assets     Assets
	tenancy    tenancy.Tenancy
	streams    Streams
	linked     Linked
	authAPI    AuthAPI
	published  Published
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

// WithBackend is where work is queued. Required, and an interface: this role never
// builds its own route to the backend.
func WithBackend(b Backend) Option {
	return func(o *options) error {
		if b == nil {
			return errors.New("build: no backend to queue into")
		}
		o.backend = b
		return nil
	}
}

// WithVault is where secret values are written. Optional: without it the secret
// routes answer as a denial does.
func WithVault(v Vault) Option {
	return func(o *options) error {
		if v == nil {
			return errors.New("build: no vault to write secrets into")
		}
		o.vault = v
		return nil
	}
}

// WithDomains is where a domain claim is signed. Optional: without it the
// claim route answers the 404 a denial gets.
func WithDomains(d Domains) Option {
	return func(o *options) error {
		if d == nil {
			return errors.New("build: no domains to claim with")
		}
		o.domains = d
		return nil
	}
}

// WithLinker records linked projects and grants in the register, after
// authorization. Optional: without it the project routes answer 404.
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

// WithHTTPServer hands the caller the *http.Server before it serves, for knobs
// this package does not name; Handler and the timeouts are already set.
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
// hands over the cloud's certificates for `build.<cloud fqdn>`,
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

// WithClock replaces the time source for the hook cache and rate limiters.
func WithClock(now func() time.Time) Option {
	return func(o *options) error {
		if now == nil {
			return errors.New("build: clock must not be nil")
		}
		o.now = now
		return nil
	}
}

// WithLogger replaces where this service says what it refused and why; most
// refusals answer a stranger with only a status, so the log is the only place the
// reason exists.
func WithLogger(logf func(format string, args ...any)) Option {
	return func(o *options) error {
		if logf == nil {
			return errors.New("build: logger must not be nil")
		}
		o.logf = logf
		return nil
	}
}

// New validates and allocates and binds nothing until Start. Every required
// dependency is an interface injected through a With… that refuses nil.
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
	case effective.Uploads != "" && o.linker == nil:
		return nil, errors.New("build: uploads need a linker to name the repository an upload is a commit of")
	case o.callers == nil:
		return nil, errors.New("build: no way to verify a caller")
	}

	var held *uploads.Dir
	if effective.Uploads != "" {
		if held, err = uploads.Open(effective.Uploads); err != nil {
			return nil, fmt.Errorf("build: uploads: %w", err)
		}
	}

	s := &Service{
		uploads:    held,
		hooks:      o.hooks,
		deliveries: o.deliveries,
		projects:   o.projects,
		callers:    o.callers,
		backend:    o.backend,
		vault:      o.vault,
		domains:    o.domains,
		linker:     o.linker,
		assets:     o.assets,
		tenancy:    o.tenancy,
		streams:    o.streams,
		linked:     o.linked,
		authAPI:    o.authAPI,
		published:  o.published,
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
	// A bucket lives one refill period. The two keyed maps are separate so a flood of
	// invented keys on one surface cannot evict the other's buckets.
	s.hookBuckets = newCache[*bucket](s.cfg.SecretCacheMax, max(s.cfg.HookRate.Per, time.Second), s.now)
	s.callerBuckets = newCache[*bucket](s.cfg.SecretCacheMax, max(s.cfg.CallerRate.Per, time.Second), s.now)
	s.globalHooks = newBucket(s.cfg.WebhookRate, s.now)
	s.globalJobs = newBucket(s.cfg.JobRate, s.now)
	s.router = s.cors(s.mount())

	return s, nil
}

// Handler is the routes with no listener, for tests and for a node terminating
// TLS elsewhere.
func (s *Service) Handler() http.Handler { return s.router }

// Start binds cfg.Listen and serves an http.Server only; twice or after Close is
// an error.
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
		// Without it a connection that never finishes its headers holds a goroutine
		// forever.
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

// Close is idempotent: it gives in-flight requests ShutdownTimeout, then takes the
// listener regardless. It closes nothing it was handed.
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

// WithPublished is how a deployment's compiled object is read back for
// `tau build --published`. Optional: without one that route is a 404.
func WithPublished(p Published) Option {
	return func(o *options) error {
		if p == nil {
			return errors.New("build: nil published")
		}
		o.published = p
		return nil
	}
}
