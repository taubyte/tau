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
// # Where it listens is an operator's, for now
//
// The kind declares no port and no address on purpose: both are allocated and
// clients are told through DNS, because at L4 the port IS the address and an
// authored one would be the first value in the whole config with a global
// collision domain.
//
// NOTHING ALLOCATES ONE YET. Until something does, an operator maps a socket to
// a proxy resource, which is exactly what they already do for a gateway — whose
// `http: ":8080"` is bound by the composer and whose name reaches it because
// somebody pointed a record at the box. Holding this kind to a stricter bar than
// the one that ships would be inconsistent, and the Bind below is the honest
// shape of "not allocated yet" rather than a config that pretends to be one.
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
	"sync"
	"time"

	"github.com/taubyte/tau/pkg/project"
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
	Listen string

	Project     string
	Environment string
	Name        string

	// Scope is the namespace chain holding the proxy, outermost first.
	Scope []string
}

// Config is what an operator declared.
type Config struct {
	// ConnectTimeout bounds reaching the workload — NOT the connection, which
	// has no duration this service could know. A psql session is measured in
	// hours.
	ConnectTimeout time.Duration `yaml:"connectTimeout"`
}

// DefaultConnectTimeout is how long reaching a workload may take.
const DefaultConnectTimeout = 10 * time.Second

var (
	ErrNoWorkers  = errors.New("proxy: a proxy with no workers can reach nothing")
	ErrNoProjects = errors.New("proxy: a proxy with no register cannot read a project")

	// ErrUnservable is what this cloud cannot do with a resource as written.
	ErrUnservable = errors.New("proxy: this cloud cannot serve that proxy as declared")
)

type Option func(*options) error

type options struct {
	workers  Workers
	projects Projects
	warn     func(string, ...any)
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

	mu        sync.Mutex
	listeners []net.Listener
	closed    bool
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
	return &Service{cfg: cfg, opts: o}, nil
}

// Serve binds one socket and carries every connection on it to the proxy's
// forward target.
func (s *Service) Serve(ctx context.Context, b Bind) error {
	listener, err := net.Listen("tcp", b.Listen)
	if err != nil {
		return fmt.Errorf("proxy: listening on %s for %s: %w", b.Listen, b.Name, err)
	}
	return s.accept(ctx, listener, b)
}

// Accept carries every connection on an already-bound listener. It is what a
// test uses, and what a composer with its own socket would.
func (s *Service) Accept(ctx context.Context, l net.Listener, b Bind) error {
	return s.accept(ctx, l, b)
}

func (s *Service) accept(ctx context.Context, l net.Listener, b Bind) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		l.Close()
		return errors.New("proxy: closed")
	}
	s.listeners = append(s.listeners, l)
	s.mu.Unlock()

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go s.carry(ctx, conn, b)
		}
	}()
	return nil
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
	proj, _, err := s.opts.projects.At(ctx, b.Project, b.Environment)
	if err != nil {
		return project.Address{}, "", err
	}

	r, id, err := proj.Resource(b.Scope, "proxy", b.Name)
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
	for _, l := range s.listeners {
		l.Close()
	}
	s.listeners = nil
	return nil
}
