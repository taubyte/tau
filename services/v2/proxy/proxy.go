// Package proxy serves the vocabulary's `proxy` kind: a raw TCP connection
// accepted at the edge and carried to a workload's listening socket.
//
// # The front door for everything that is not HTTP
//
// A gateway can only route what it can parse — a path, a method, a Host header —
// and Postgres, Redis, MQTT and a game server offer none of those. This accepts
// a connection, decides nothing about what is inside it, and moves the bytes.
//
// # It lives under services/v2 because the word is taken
//
// `services/proxy` is the HTTP INGRESS — client to dns to proxy to node — and
// shares only the word. The vocabulary's word belongs to the vocabulary's kind,
// and the same disambiguation `services/v2/gateway` and `services/v2/auth`
// already use applies here.
//
// # Nothing here names an address or a client port
//
// A proxy listens on every address this node has, on a port the kernel picked,
// and clients find that port through DNS. No address is authored anywhere,
// because an address has to be routed to a machine before it can be bound —
// see socket.go. The only port in the vocabulary is the one a container binds
// inside itself, which never leaves the machine it runs on.
//
// # What it refuses, by name
//
// A connection is closed with nothing written when this cloud cannot serve what
// the resource declares. There is nowhere to put a reason — a raw socket has no
// status line — so the reason goes to the operator's log and the client gets
// what any unreachable service gives it.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/taubyte/tau/pkg/project"
	"github.com/taubyte/tau/pkg/ratelimit"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/splice"
)

// Workers opens a connection to a workload, wherever it runs.
//
// THE PROXY DOES NOT CHOOSE A NODE, for the reason the gateway's own Workers
// states: which node answers is a routing decision made from live measurements,
// and half of it here would be half of it in two places.
type Workers interface {
	Conn(ctx context.Context, at project.Address, port string) (net.Conn, error)
}

// Projects reads a deployment's compiled project.
type Projects interface {
	At(ctx context.Context, project, environment string) (*project.Project, register.Pointer, error)
}

// Bind is one socket and the proxy resource it serves.
//
// IT NAMES THE RESOURCE because a raw connection carries nothing that could
// identify one. That is the whole reason the vocabulary gives each proxy its own
// address: at L4 there is no Host header to demultiplex on, so one listener
// serves exactly one proxy.
type Bind struct {
	Project     string
	Environment string
	Name        string

	// Scope is the namespace chain holding the proxy, outermost first.
	Scope []string
}

// Key is what tells two proxies apart across deployments and namespaces: the
// four fields joined, none of which may hold a "/". It exists so that what is
// already being served can be held in a map without a second spelling of one
// resource's identity.
func (b Bind) Key() string {
	parts := make([]string, 0, len(b.Scope)+3)
	parts = append(parts, b.Project, b.Environment)
	parts = append(parts, b.Scope...)
	return strings.Join(append(parts, b.Name), "/")
}

// Config is what an operator declared.
type Config struct {
	// ConnectTimeout bounds reaching the workload — NOT the connection, which
	// has no duration this service could know. A psql session is measured in
	// hours.
	ConnectTimeout time.Duration `yaml:"connectTimeout"`

	// Sweep is how often this node compares what it is listening for to what
	// it holds. Default DefaultSweep.
	Sweep time.Duration `yaml:"sweep"`

	// MaxSources bounds the rate-limit table. Default DefaultMaxSources.
	//
	// A PROXY UNDER A SPRAY OF ONE-CONNECTION-PER-ADDRESS TRAFFIC would
	// otherwise grow a bucket per source forever, which turns a limit meant to
	// shed load into a way to spend this node's memory.
	MaxSources int `yaml:"maxSources"`

	// CloudFqdn is the zone this cloud serves, e.g. "tau.example.io".
	//
	// IT IS THE ONE PIECE OF DNS THIS SERVICE IS TOLD, and it is operator
	// config for the same reason the gateway's field of this name is: a
	// generated host compiles to `<label>.<project label>` and the rest of it
	// is not the project's to assert. With none, only the names whose zones the
	// projects themselves declare can be published.
	CloudFqdn string `yaml:"cloudFqdn"`
}

// DefaultConnectTimeout is how long reaching a workload may take.
const DefaultConnectTimeout = 10 * time.Second

// DefaultMaxSources is how many sources a proxy remembers a rate for.
const DefaultMaxSources = 4096

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
	// as no source at all — see WithDeployments.
	ErrNoDeployments = errors.New("proxy: a nil source holds no proxies")

	// ErrUnservable is what this cloud cannot do with a resource as written.
	ErrUnservable = errors.New("proxy: this cloud cannot serve that proxy as declared")
)

type Option func(*options) error

type options struct {
	workers     Workers
	projects    Projects
	deployments Deployments
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

	// refusals is the last reason each proxy could not be served, so a sweep
	// every ten seconds does not repeat one sentence until it buries the
	// deploy that fixes it.
	refusals map[string]string

	// buckets is how fast connections may arrive, per proxy per source; open
	// is how many may be held at once. Two limits because a connection that is
	// HELD is not counted by a rate — see limits.go.
	buckets *ratelimit.Buckets
	open    *ratelimit.Gauge
}

// bound is one proxy this node is currently accepting connections for.
type bound struct {
	listener net.Listener
	socket   Socket

	// limits is what one source may open on it. It is held here rather than
	// read per connection because it has to be consulted BEFORE the project
	// is — a limit that cost a project read to enforce would be load rather
	// than a way to shed it. The sweep refreshes it, so an edited limit takes
	// effect within one.
	limits limits
}

// Serving is every proxy this node accepts connections for, for whatever
// reports this node to the fleet. It is the whole of what DNS needs from here:
// the name, what a client looks it up as, and the port the kernel gave it.
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

// Serve binds this proxy and carries every connection on it.
//
// IT ANSWERS WHERE IT LANDED, because that is what DNS has to publish: an SRV
// carrying the port this proxy actually got, beside the addresses the node
// already reports. Serving() is the same answer for everything this service
// holds, which is what a periodic report reads.
func (s *Service) Serve(ctx context.Context, b Bind) (Socket, error) {
	proj, r, id, err := s.resource(ctx, b)
	if err != nil {
		return Socket{}, err
	}
	if why := unservable(r); why != "" {
		return Socket{}, fmt.Errorf("%w: %s", ErrUnservable, why)
	}

	l, sock, err := s.listen(proj, r, id, b.Name)
	if err != nil {
		return Socket{}, err
	}
	if err := s.accept(ctx, l, sock, limitsOf(r), b); err != nil {
		return Socket{}, err
	}
	return sock, nil
}

// resource reads this proxy out of its deployment.
func (s *Service) resource(ctx context.Context, b Bind) (*project.Project, project.Resource, string, error) {
	proj, _, err := s.opts.projects.At(ctx, b.Project, b.Environment)
	if err != nil {
		return nil, nil, "", err
	}
	r, id, err := proj.Resource(b.Scope, "proxy", b.Name)
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

			// THE BUDGET IS SPENT BEFORE THE PROJECT IS READ. A refused
			// connection costs a map lookup and a close — a raw socket has no
			// status line, so there is nothing to write and nothing is
			// written, and nothing is logged either: a line per refusal is a
			// log amplifier pointed at this node by whoever is being refused.
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

// carry takes one connection to the workload the proxy forwards to.
func (s *Service) carry(ctx context.Context, from net.Conn, b Bind) {
	at, port, err := s.forwards(ctx, b)
	if err != nil {
		// A RAW SOCKET HAS NO STATUS LINE, so the client gets what any
		// unreachable service gives it and the reason goes to the operator.
		s.opts.warn("proxy: %s/%s: %s", b.Environment, b.Name, err)
		from.Close()
		return
	}

	dial, cancel := context.WithTimeout(ctx, s.cfg.ConnectTimeout)
	defer cancel()

	to, err := s.opts.workers.Conn(dial, at, port)
	if err != nil {
		s.opts.warn("proxy: %s/%s reaching %s/%s: %s", b.Environment, b.Name, at.Kind, at.Name, err)
		from.Close()
		return
	}

	splice.Both(from, to)
	from.Close()
	to.Close()
}

// forwards is where this proxy's connections go, and whether they may go at all.
func (s *Service) forwards(ctx context.Context, b Bind) (project.Address, string, error) {
	proj, r, id, err := s.resource(ctx, b)
	if err != nil {
		return project.Address{}, "", err
	}
	if why := unservable(r); why != "" {
		return project.Address{}, "", fmt.Errorf("%w: %s", ErrUnservable, why)
	}

	// WHERE THE PROXY ITSELF IS, which is not where the operator's bind says:
	// a forward's omitted namespace means the proxy's own scope and then its
	// parents, which is the rule servedBy states for a channel.
	_, scope, _, ok := proj.Locate(id)
	if !ok {
		return project.Address{}, "", fmt.Errorf("proxy %q could not be located in this project", b.Name)
	}

	kind := r.String("forward", "kind")
	name := r.String("forward", "name")
	if kind == "" || name == "" {
		return project.Address{}, "", fmt.Errorf("%w: it forwards to nothing", ErrUnservable)
	}

	return project.Address{
		Project:     b.Project,
		Environment: b.Environment,
		Scope:       project.ScopeOf(scope, r.String("forward", "namespace")),
		Kind:        kind,
		Name:        name,
	}, r.String("forward", "port"), nil
}

// unservable is what this resource declares that this cloud cannot do.
//
// EVERY ONE OF THESE IS A REFUSAL RATHER THAN A DOWNGRADE. A proxy that asked
// for TLS and got a plaintext pipe would be a security control silently not
// applied, and one that asked for udp and got tcp would carry a protocol that
// chose to do without ordering over something that imposes it.
func unservable(r project.Resource) string {
	if p := r.String("protocol"); p != "" && p != "tcp" {
		return fmt.Sprintf("it carries %q, and this cloud carries tcp — a stream is reliable and ordered, "+
			"which is the property a udp application chose to do without", p)
	}
	if mode := r.String("tls", "mode"); mode == "terminate" {
		return "it terminates TLS, and nothing here holds a certificate for a proxy yet"
	}
	if r.String("routing") == "router" {
		return "a function decides where its connections go, and this cloud runs no proxy router yet"
	}
	if n := len(r.List("transformers")); n > 0 {
		return fmt.Sprintf("%d transformer(s) shape its bytes, and this cloud runs no proxy transformer yet", n)
	}
	return ""
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
