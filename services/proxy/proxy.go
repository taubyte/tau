// Package proxy serves the vocabulary's `proxy` kind: a raw TCP connection accepted
// at the edge and carried to a workload's listening socket, for protocols a gateway
// cannot parse (Postgres, Redis, MQTT, game servers).
//
// A proxy listens on every address of the node, on a kernel-picked port that clients
// find through DNS (socket.go). It terminates TLS itself, since it is the socket
// (tls.go). `routing: router` lets a function choose the destination from the
// source address and leading bytes (route.go); `transformers:` pass the bytes
// through functions in both directions (shape.go). Both run on a worker.
//
// A connection that cannot be served is closed with nothing written, since a raw
// socket has no status line; the reason goes to the operator's log. Rate-limit
// refusals are not logged (limits.go).
package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/taubyte/tau/pkg/certificate"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/ratelimit"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/pkg/splice"
)

// Workers opens a connection to a workload, wherever it runs. The proxy does not
// choose a node: that is a routing decision made from live measurements.
type Workers interface {
	Conn(ctx context.Context, at specs.Address, port string) (net.Conn, error)

	// Direct asks a proxy router where one connection goes, and Shape runs one
	// chunk of it through a transformer. Both are placed as the workload is.
	Direct(ctx context.Context, at specs.Address, conn *invoke.Connection, peeked []byte) (*invoke.Direction, error)
	Shape(ctx context.Context, at specs.Address, s *invoke.Stream) (*invoke.Shaping, error)
}

// Projects reads a deployment's compiled project.
type Projects interface {
	At(ctx context.Context, at register.Deployment) (*specs.Project, register.Pointer, error)
}

// Bind is one socket and the proxy resource it serves. At L4 there is no Host header
// to demultiplex on, so one listener serves exactly one proxy.
type Bind struct {
	Project     string
	Environment string
	Name        string

	// Scope is the namespace chain holding the proxy, outermost first.
	Scope []string
}

// Key tells two proxies apart across deployments and namespaces: the four fields
// joined, none of which may hold a "/".
func (b Bind) Key() string {
	parts := make([]string, 0, len(b.Scope)+3)
	parts = append(parts, b.Project, b.Environment)
	parts = append(parts, b.Scope...)
	return strings.Join(append(parts, b.Name), "/")
}

// Config is what an operator declared.
type Config struct {
	// ConnectTimeout bounds reaching the workload, not the connection: a psql
	// session lasts hours.
	ConnectTimeout time.Duration `yaml:"connectTimeout"`

	// Sweep is how often this node compares what it is listening for to what
	// it holds. Default DefaultSweep.
	Sweep time.Duration `yaml:"sweep"`

	// Wildcard is the certificate for this cloud's own zone, which a generated host
	// terminates TLS with. No project can hold a key for that zone.
	Wildcard Certificate `yaml:"wildcard"`

	// Peek is how many bytes a router may ask to see before deciding, and
	// Decide is how long the whole decision may take. Defaults DefaultPeek and
	// DefaultDecide. Neither is the router's to raise: a router asking for more is a
	// client holding the listener open by sending nothing.
	Peek   int           `yaml:"peek"`
	Decide time.Duration `yaml:"decide"`

	// MaxSources bounds the rate-limit table, so a spray of one connection per address
	// cannot spend this node's memory. Default DefaultMaxSources.
	MaxSources int `yaml:"maxSources"`

	// CloudFqdn is the zone this cloud serves, e.g. "tau.example.io". A generated host
	// compiles to `<label>.<project label>` and the rest is not the project's to assert.
	// With none, only names in zones the projects declare can be published.
	CloudFqdn string `yaml:"cloudFqdn"`
}

// Certificate is a chain and its key, in PEM.
type Certificate = certificate.Pair

// DefaultConnectTimeout is how long reaching a workload may take.
const DefaultConnectTimeout = 10 * time.Second

// DefaultMaxSources is how many sources a proxy remembers a rate for.
const DefaultMaxSources = 4096

func (c Config) peekLimit() int {
	if c.Peek < 1 {
		return DefaultPeek
	}
	return c.Peek
}

func (c Config) decideTimeout() time.Duration {
	if c.Decide <= 0 {
		return DefaultDecide
	}
	return c.Decide
}

func (c Config) maxSources() int {
	if c.MaxSources < 1 {
		return DefaultMaxSources
	}
	return c.MaxSources
}

var (
	ErrNoWorkers  = errors.New("proxy: a proxy with no workers can reach nothing")
	ErrNoProjects = errors.New("proxy: a proxy with no register cannot read a project")

	// ErrNoDeployments is a nil source of what to serve, which is not the same
	// as no source at all; see WithDeployments.
	ErrNoDeployments = errors.New("proxy: a nil source holds no proxies")

	// ErrUnservable is a resource this cloud cannot serve as written: a
	// certificate it has no way to obtain, a forward that names nothing, a
	// router it cannot find.
	ErrUnservable = errors.New("proxy: this cloud cannot serve that proxy as declared")
)

type Option func(*options) error

type options struct {
	workers     Workers
	projects    Projects
	deployments Deployments
	secrets     Secrets
	warn        func(string, ...any)
}

func WithWorkers(w Workers) Option {
	return func(o *options) error {
		if w == nil {
			return ErrNoWorkers
		}
		o.workers = w
		return nil
	}
}

func WithProjects(p Projects) Option {
	return func(o *options) error {
		if p == nil {
			return ErrNoProjects
		}
		o.projects = p
		return nil
	}
}

// WithSecrets is how a proxy reads the certificate a resource names. A service
// without one cannot serve `certificate: {source: provided}`, and says so
// rather than serving plaintext.
func WithSecrets(sec Secrets) Option {
	return func(o *options) error {
		if sec == nil {
			return errors.New("proxy: a nil secret resolver reads nothing")
		}
		o.secrets = sec
		return nil
	}
}

// WithWarn is where a refused connection says why. A raw socket has no status
// line, so this is the only place a reason can go.
func WithWarn(f func(string, ...any)) Option {
	return func(o *options) error {
		if f == nil {
			return errors.New("proxy: a nil warn writes nowhere")
		}
		o.warn = f
		return nil
	}
}

// Service accepts connections for proxy resources.
type Service struct {
	cfg  Config
	opts options

	mu     sync.Mutex
	served map[string]*bound
	closed bool

	// refusals is the last reason each proxy could not be served, so a sweep does not
	// repeat it every ten seconds.
	refusals map[string]string

	// buckets is how fast connections may arrive, per proxy per source; open
	// is how many may be held at once.
	buckets *ratelimit.Buckets
	open    *ratelimit.Gauge
}

// bound is one proxy this node is currently accepting connections for.
type bound struct {
	listener net.Listener
	socket   Socket

	// limits is what one source may open on it. It is held here because it is consulted
	// before the project is read; the sweep refreshes it.
	limits limits
}

// Serving is every proxy this node accepts connections for: what DNS needs, the
// name, what a client looks up, and the port the kernel gave it.
func (s *Service) Serving() []Socket {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Socket, 0, len(s.served))
	for _, b := range s.served {
		if b.socket.Host != "" {
			out = append(out, b.socket)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		return out[i].Protocol < out[j].Protocol
	})
	return out
}

func New(cfg Config, opts ...Option) (*Service, error) {
	var o options
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, err
		}
	}
	if o.workers == nil {
		return nil, ErrNoWorkers
	}
	if o.projects == nil {
		return nil, ErrNoProjects
	}
	if o.warn == nil {
		o.warn = func(string, ...any) {}
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = DefaultConnectTimeout
	}
	return &Service{
		cfg:      cfg,
		opts:     o,
		served:   map[string]*bound{},
		refusals: map[string]string{},
		buckets:  ratelimit.NewBuckets(nil, cfg.maxSources()),
		open:     ratelimit.NewGauge(),
	}, nil
}

// Serve binds this proxy and carries every connection on it. It answers where it
// landed, which DNS publishes as an SRV record.
func (s *Service) Serve(ctx context.Context, b Bind) (Socket, error) {
	proj, r, id, err := s.resource(ctx, b)
	if err != nil {
		return Socket{}, err
	}
	l, sock, err := s.listen(proj, r, id, b.Name)
	if err != nil {
		return Socket{}, err
	}

	// The certificate is resolved after the bind and before anything is accepted, so a
	// proxy that asked for TLS and cannot have it holds no port.
	cfg, err := s.certificateFor(ctx, r, b, sock.Host)
	if err != nil {
		l.Close()
		return Socket{}, err
	}
	if cfg != nil {
		l = tls.NewListener(l, cfg)
	}

	if err := s.accept(ctx, l, sock, limitsOf(r), b); err != nil {
		return Socket{}, err
	}
	return sock, nil
}

// resource reads this proxy out of its deployment.
func (s *Service) resource(ctx context.Context, b Bind) (*specs.Project, specs.Resource, string, error) {
	proj, _, err := s.opts.projects.At(ctx, register.Deployment{Project: b.Project, Environment: b.Environment})
	if err != nil {
		return nil, nil, "", err
	}
	r, id, err := proj.Resource(b.Scope, specs.KindProxy, b.Name)
	return proj, r, id, err
}

// Accept carries every connection on an already-bound listener. It is what a
// test uses, and what a composer with its own socket would.
func (s *Service) Accept(ctx context.Context, l net.Listener, b Bind) error {
	return s.accept(ctx, l, Socket{}, limits{}, b)
}

func (s *Service) accept(ctx context.Context, l net.Listener, sock Socket, lim limits, b Bind) error {
	key := b.Key()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		l.Close()
		return errors.New("proxy: closed")
	}
	if held, ok := s.served[key]; ok {
		// ONE LISTENER PER RESOURCE. Two would each get half the connections
		// and only one of them would be in DNS.
		s.mu.Unlock()
		l.Close()
		return fmt.Errorf("proxy: %s is already served on %s", key, held.socket.Host)
	}
	s.served[key] = &bound{listener: l, socket: sock, limits: lim}
	s.mu.Unlock()

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}

			// The budget is spent before the project is read, and a refusal is neither
			// written to nor logged.
			done, ok := s.admit(key, s.limitsFor(key), conn.RemoteAddr())
			if !ok {
				conn.Close()
				continue
			}

			go func() {
				defer done()
				s.carry(ctx, conn, b)
			}()
		}
	}()
	return nil
}

// limitsFor is what the last sweep read off this proxy.
func (s *Service) limitsFor(key string) limits {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.served[key]; ok {
		return b.limits
	}
	return limits{}
}

// carry takes one connection to wherever it goes: route, then shape (route.go says
// why). The transformers see the whole stream, the router's peek included.
func (s *Service) carry(ctx context.Context, from net.Conn, b Bind) {
	defer from.Close()

	proj, r, id, err := s.resource(ctx, b)
	if err != nil {
		// A raw socket has no status line, so the reason goes to the operator.
		s.opts.warn("proxy: %s/%s: %s", b.Environment, b.Name, err)
		return
	}

	// Where the proxy itself is, not the bind: an omitted namespace means the proxy's
	// own scope and then its parents.
	_, scope, _, ok := proj.Locate(id)
	if !ok {
		s.opts.warn("proxy: %s could not be located in this project", b.Name)
		return
	}

	conn := s.connectionOf(r, b, from)

	var (
		at     specs.Address
		port   string
		peeked []byte
	)
	if specs.ReadProxy(r).Routing == specs.ProxyRoutingRouter {
		at, port, peeked, err = s.directed(ctx, r, b, scope, conn, from)
	} else {
		at, port, err = s.forwardedTo(r, b, scope)
	}
	if err != nil {
		s.opts.warn("proxy: %s/%s: %s", b.Environment, b.Name, err)
		return
	}

	dial, cancel := context.WithTimeout(ctx, s.cfg.ConnectTimeout)
	defer cancel()

	to, err := s.opts.workers.Conn(dial, at, port)
	if err != nil {
		s.opts.warn("proxy: %s/%s reaching %s/%s: %s", b.Environment, b.Name, at.Kind, at.Name, err)
		return
	}
	defer to.Close()

	inbound := s.chain(ctx, r, b, scope, conn, invoke.Inbound)
	outbound := s.chain(ctx, r, b, scope, conn, invoke.Outbound)

	// What the router read is still the client's bytes, and it goes through the chain
	// so a transformer sees the protocol's greeting.
	if len(peeked) > 0 {
		out := peeked
		if inbound != nil {
			if out, err = inbound(peeked, false); err != nil {
				s.opts.warn("proxy: %s/%s: %s", b.Environment, b.Name, err)
				return
			}
		}
		if len(out) > 0 {
			if _, err := to.Write(out); err != nil {
				return
			}
		}
	}

	if err := splice.Through(from, to, inbound, outbound); err != nil {
		s.opts.warn("proxy: %s/%s: %s", b.Environment, b.Name, err)
	}
}

// connectionOf is what a router or a transformer is told about this connection: a
// peer address, the name it arrived on, and the handshake if there was one.
func (s *Service) connectionOf(r specs.Resource, b Bind, from net.Conn) *invoke.Connection {
	// The labels come off the resource and the name off the listener, which alone knows
	// what the cloud's zone made of a generated host. Without a listener entry (Accept)
	// the name is empty.
	declared := specs.ReadProxy(r)
	conn := &invoke.Connection{Protocol: declared.Service, Transport: Transport}
	if conn.Protocol == "" {
		conn.Protocol = declared.Name
	}
	if conn.Protocol == "" {
		conn.Protocol = b.Name
	}
	if held, ok := s.heldSocket(b.Key()); ok {
		conn.Host = held.Host
	}
	if addr := from.RemoteAddr(); addr != nil {
		conn.Source = addr.String()
	}
	if tlsConn, ok := from.(*tls.Conn); ok {
		// Completing the handshake here exposes the requested name to a router without it
		// parsing a ClientHello.
		if err := tlsConn.HandshakeContext(context.Background()); err == nil {
			conn.ServerName = tlsConn.ConnectionState().ServerName
		}
	}
	return conn
}

// heldSocket is where this proxy ended up listening, for whoever needs the name
// it answers on.
func (s *Service) heldSocket(key string) (Socket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.served[key]; ok {
		return b.socket, true
	}
	return Socket{}, false
}

// forwardedTo is the workload a `routing: forward` proxy streams to.
func (s *Service) forwardedTo(r specs.Resource, b Bind, scope []string) (specs.Address, string, error) {
	forward := specs.ReadProxy(r).Forward
	kind, name := forward.Kind, forward.Name
	if kind == "" || name == "" {
		return specs.Address{}, "", fmt.Errorf("%w: it forwards to nothing", ErrUnservable)
	}

	return specs.Address{
		Project:     b.Project,
		Environment: b.Environment,
		Scope:       specs.ScopeOf(scope, forward.Namespace),
		Kind:        kind,
		Name:        name,
	}, forward.Port, nil
}

// Close stops accepting. Connections already open are left alone: a proxy that
// cut live sessions to reload would be worse than one that stops taking new
// ones.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true
	for _, b := range s.served {
		b.listener.Close()
	}
	clear(s.served)
	clear(s.refusals)
	return nil
}
