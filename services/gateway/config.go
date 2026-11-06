package gateway

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/taubyte/tau/pkg/certificate"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
)

// Every interface here is declared by the consumer and as narrow as the use.

// Hosts answers whose environment a name belongs to. It is one read, and it is
// the first thing that happens to a request.
//
// Both *register.Register and a register client satisfy it, so a node running
// both wires them together with no network.
type Hosts interface {
	ResolveHost(ctx context.Context, fqdn string) (project, environment string, err error)
}

// Workers runs a request against a resource.
//
// THE GATEWAY DOES NOT CHOOSE A NODE. Which worker answers is a routing
// decision made from live measurements (services/discovery), and putting it behind
// this interface is what keeps that decision in one place instead of half here.
// *services/worker.Client satisfies it for a fixed peer; a placement-aware
// implementation satisfies it for a fleet.
type Workers interface {
	Invoke(ctx context.Context, at specs.Address, req *invoke.Request) (*invoke.Response, error)

	// Transform runs one request through a transformer — a function this
	// gateway declares that traffic passes through on its way in.
	//
	// IT GOES TO A WORKER RATHER THAN RUNNING HERE, which is the whole reason
	// it is on this interface. A transformer is a workload of the same
	// deployment, so the node that is warm for the project is warm for its
	// transformers; a gateway that ran them itself would need a component tier
	// on every edge node and would lose that warmth.
	Transform(ctx context.Context, at specs.Address, req *invoke.Request) (*invoke.Outcome, error)

	// Route asks a router where one request should go. Same reasons as
	// Transform: a router is a workload of this deployment and decides per
	// request, so it runs where the project is warm.
	Route(ctx context.Context, at specs.Address, req *invoke.Request) (*invoke.Routing, error)

	// Conn opens a connection to one of a workload's ports, by the name it
	// declared — what a route that names a port is delivered over. See
	// port.go.
	Conn(ctx context.Context, at specs.Address, port string) (net.Conn, error)
}

// Buses attaches a caller to an ephemeral bus.
//
// IT IS NOT Workers. Invoking is request-and-answer and ends; an attachment is
// held open for as long as the browser is there, and nothing about it is
// routed — every node that can reach the swarm reaches the same topic, so the
// node that accepted the connection is the node that serves it.
type Buses interface {
	// Attach opens one caller's view of the bus a handle names. The handle is
	// specs.Handle of the resolved resource, which is what a workload's
	// grant carries — the two have to agree or a browser and a workload never
	// meet.
	Attach(ctx context.Context, handle string) (Attachment, error)
}

// Admits decides whether a browser may attach to one instance of a bus.
//
// IT IS THE VOCABULARY'S OWN CONTROL and the only one this kind has. A bus with
// a `host:` is reachable from the internet; `instances:` is where the project
// says WHICH of them may exist and who decides — "the thing attaching is a
// BROWSER, so the instance name is the caller's, and the project has to have a
// point at which it says no". Without this, the only buses a cloud could serve
// were the ones that named nobody.
type Admits interface {
	// AdmitInstance runs the bus's validator for one instance. The handle is
	// the resolved resource; the settings are the compiled `instances:` block.
	//
	// THE METHOD IS NAMED FOR WHAT IT ADMITS rather than being a bare Admit,
	// for the reason KVs.OpenKV is: one type answers this and the tier's own
	// admission at once, and Go cannot have two methods of one name.
	AdmitInstance(ctx context.Context, handle string, settings map[string]string, instance string) error

	// AdmitRequest asks a route's guard whether one request may pass, and with
	// what ops. `at` names the FUNCTION the route or the gateway declared.
	//
	// IT ANSWERS WITH A SET rather than a bool because a route to a bus needs
	// "may listen, may not send" to be sayable — an attachment is a standing
	// subscription, and granting it wholesale is not the same decision as
	// letting somebody read.
	AdmitRequest(ctx context.Context, at specs.Address, req *invoke.Request) (*invoke.Admission, error)
}

// Attachment is one connection's view of a bus: what arrives, and what it
// sends. Close releases it and nobody else's.
type Attachment interface {
	// Next blocks until a message arrives, ctx ends, or the attachment closes.
	Next(ctx context.Context) ([]byte, error)

	// Publish sends one message to everything attached, INCLUDING THE SENDER.
	// A bus is n:n and that is what attached means; a client that wants to
	// ignore its own traffic puts something in the payload that says so,
	// because an envelope added here would be a wire format a workload
	// publishing to the same topic does not write.
	Publish(ctx context.Context, message []byte) error

	Close() error
}

// Config is what an operator declared, and ONLY that.
type Config struct {
	// CloudFqdn is the zone this cloud serves, e.g. "tau.example.io".
	//
	// IT IS OPERATOR CONFIG AND NOT PROJECT CONFIG, which is the whole reason a
	// generated host is incomplete in a compiled project: `<label>.<project
	// label>` is what the config can know, and this is the rest. A gateway with
	// none serves only the names whose zones the projects themselves declare. It is
	// the node's cloudfqdn, set by the instance, never a key of its own.
	CloudFqdn string `yaml:"-"`

	// MaxProjects bounds how many compiled projects are held. Default
	// project.DefaultMax.
	MaxProjects int

	// MaxHosts bounds the rate limiter's table of keys. Default 4096. Who owns a
	// name is kept by the register reader the gateway resolves it through, until
	// the register says it moved.
	MaxHosts int

	// Timeout bounds one request end to end. Default 30s.
	Timeout time.Duration

	// MaxBody is the largest request body accepted, in bytes. Default 32MB.
	//
	// A LIMIT IS NOT OPTIONAL HERE. The invocation model buffers a whole body
	// before the workload sees any of it, so an unbounded upload is an
	// unbounded allocation on a public listener.
	MaxBody int64

	// FilesBudget bounds the bytes of package files this gateway keeps unpacked
	// on disk at once. Default DefaultFilesBudget.
	FilesBudget int64

	// Wildcard is the certificate for this cloud's own zone, which is what a
	// generated name answers a handshake with.
	//
	// IT IS THE OPERATOR'S FOR THE REASON CloudFqdn IS: a generated name ends
	// in a zone no project declares, so no project can hold a key for it. A
	// node with none terminates only the names whose gateways provide a
	// certificate of their own. See tls.go.
	Wildcard certificate.Pair
}

const (
	DefaultMaxHosts = 4096
	DefaultTimeout  = 30 * time.Second
	DefaultMaxBody  = 32 << 20
)

var (
	ErrNoHosts   = errors.New("gateway: a gateway with no host table cannot tell whose a request is")
	ErrNoWorkers = errors.New("gateway: a gateway with nothing to invoke serves nothing")
	ErrNoSource  = errors.New("gateway: a gateway with no register cannot read a project")
)

// Option injects what this service is built from.
type Option func(*options) error

type options struct {
	hosts   Hosts
	workers Workers
	source  register.Registry
	buses   Buses
	admits  Admits
	now     func() time.Time
	meter   Meter
	logf    func(format string, args ...any)

	secrets    Secrets
	issuer     Issuer
	terminates bool

	artifacts Artifacts
	assets    Assets
}

// Issuer issues a certificate for a generated name the operator's wildcard
// does not cover, and answers the CA's HTTP-01 challenges for it.
// services/cm' Client is the one a node hands over: it orders what
// nothing keeps yet and answers HTTP-01 from the cloud's account key.
type Issuer interface {
	Certificate(ctx context.Context, name string) (tls.Certificate, error)
	Challenge(token string) (string, bool)
}

// ProjectIssuer is an Issuer that also issues for a name in a project's own
// zone, once the zone is proven to be the project's. services/cm's Client is
// one.
type ProjectIssuer interface {
	ForProject(ctx context.Context, project, zone, name string) (tls.Certificate, error)
}

// WithIssuer issues certificates for generated names. Optional: without it a
// generated name is answered by the wildcard or not at all.
func WithIssuer(i Issuer) Option {
	return func(o *options) error {
		if i == nil {
			return errors.New("gateway: a nil issuer")
		}
		o.issuer = i
		return nil
	}
}

// WithHosts is where "whose name is this" is answered. Required.
func WithHosts(h Hosts) Option {
	return func(o *options) error {
		if h == nil {
			return ErrNoHosts
		}
		o.hosts = h
		return nil
	}
}

// WithWorkers is what runs a request. Required.
func WithWorkers(w Workers) Option {
	return func(o *options) error {
		if w == nil {
			return ErrNoWorkers
		}
		o.workers = w
		return nil
	}
}

// WithSource is where compiled projects are read from. Required.
func WithSource(s register.Registry) Option {
	return func(o *options) error {
		if s == nil {
			return ErrNoSource
		}
		o.source = s
		return nil
	}
}

// WithMeter is where this service says what it accounted for — the bytes that
// crossed the edge, in both directions, served and refused. Nil emits nothing.
func WithMeter(m Meter) Option {
	return func(o *options) error {
		if m == nil {
			return errors.New("gateway: a nil meter")
		}
		o.meter = m
		return nil
	}
}

// WithBuses is what attaches a browser to a bus. Optional: a gateway without
// one refuses a bus's name and serves every other kind, because attaching
// needs the swarm's gossip and a node may run a gateway without it.
func WithBuses(b Buses) Option {
	return func(o *options) error {
		if b == nil {
			return errors.New("gateway: a nil bus backing")
		}
		o.buses = b
		return nil
	}
}

// WithAdmission is what decides whether a caller may attach to one instance of
// a bus. A gateway without one refuses every bus that declares `instances:`,
// which is a security control silently absent otherwise.
func WithAdmission(a Admits) Option {
	return func(o *options) error {
		if a == nil {
			return errors.New("gateway: a nil admission")
		}
		o.admits = a
		return nil
	}
}

// WithLogger is where this service says why a route could not be served: a stranger
// is told only 502 or 504, and the operator the reason. The default says nothing.
func WithLogger(logf func(format string, args ...any)) Option {
	return func(o *options) error {
		if logf == nil {
			return errors.New("gateway: a nil logger")
		}
		o.logf = logf
		return nil
	}
}

// WithSecrets is what reads a provided certificate. Optional: a gateway
// without one refuses the handshake for a name that provides its own, and
// serves every generated one.
func WithSecrets(sec Secrets) Option {
	return func(o *options) error {
		if sec == nil {
			return errors.New("gateway: a nil secret resolver")
		}
		o.secrets = sec
		return nil
	}
}

// WithTLS says the composer serves this handler over TLS too, answering each
// handshake with Certificate. Without it a name that provides its own
// certificate is refused over plain HTTP — see tls.go.
func WithTLS() Option {
	return func(o *options) error {
		o.terminates = true
		return nil
	}
}

// WithClock injects the local clock. Tests use it; production does not.
func WithClock(now func() time.Time) Option {
	return func(o *options) error {
		if now == nil {
			return errors.New("gateway: a nil clock")
		}
		o.now = now
		return nil
	}
}

func (c Config) withDefaults() (Config, error) {
	if c.MaxHosts == 0 {
		c.MaxHosts = DefaultMaxHosts
	}
	if c.Timeout == 0 {
		c.Timeout = DefaultTimeout
	}
	if c.MaxBody == 0 {
		c.MaxBody = DefaultMaxBody
	}
	if c.FilesBudget == 0 {
		c.FilesBudget = DefaultFilesBudget
	}
	switch {
	case c.MaxHosts < 1:
		return Config{}, fmt.Errorf("gateway: %d hosts: a table that holds nothing asks on every request", c.MaxHosts)
	case c.Timeout <= 0:
		return Config{}, fmt.Errorf("gateway: timeout %s: a request needs a budget", c.Timeout)
	case c.MaxBody < 1:
		return Config{}, fmt.Errorf("gateway: %d bytes: a body limit of nothing refuses every request with a body", c.MaxBody)
	case c.FilesBudget < 1:
		return Config{}, fmt.Errorf("gateway: %d bytes: a files budget of nothing unpacks nothing", c.FilesBudget)
	}
	return c, nil
}
