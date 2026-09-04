package gateway

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/project"
)

// config.go declares what this service is built from. Every interface here is
// declared by the consumer and is as narrow as the use.

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
// decision made from live measurements (services/stats), and putting it behind
// this interface is what keeps that decision in one place instead of half here.
// *services/worker.Client satisfies it for a fixed peer; a placement-aware
// implementation satisfies it for a fleet.
type Workers interface {
	Invoke(ctx context.Context, at project.Address, req *invoke.Request) (*invoke.Response, error)
}

// Config is what an operator declared, and ONLY that.
type Config struct {
	// CloudFqdn is the zone this cloud serves, e.g. "tau.example.io".
	//
	// IT IS OPERATOR CONFIG AND NOT PROJECT CONFIG, which is the whole reason a
	// generated host is incomplete in a compiled project: `<label>.<project
	// label>` is what the config can know, and this is the rest. A gateway with
	// none serves only the names whose zones the projects themselves declare.
	CloudFqdn string

	// PointerTTL is how long a resolved pointer is trusted — the deploy
	// propagation budget. Default project.DefaultTTL.
	PointerTTL time.Duration

	// MaxProjects bounds how many compiled projects are held. Default
	// project.DefaultMax.
	MaxProjects int

	// HostTTL is how long a name's owner is remembered. Default 30s.
	//
	// SEPARATE FROM PointerTTL BECAUSE IT ANSWERS A DIFFERENT QUESTION. A
	// pointer moves on every deploy; who owns a name changes when somebody adds
	// or removes a domain, which is rarer and much more expensive to get wrong
	// — a stale answer here sends a request to the wrong PROJECT, where a stale
	// pointer only serves the previous revision of the right one.
	HostTTL time.Duration

	// MaxHosts bounds the host table. Default 4096.
	MaxHosts int

	// Timeout bounds one request end to end. Default 30s.
	Timeout time.Duration

	// MaxBody is the largest request body accepted, in bytes. Default 32MB.
	//
	// A LIMIT IS NOT OPTIONAL HERE. The invocation model buffers a whole body
	// before the workload sees any of it, so an unbounded upload is an
	// unbounded allocation on a public listener.
	MaxBody int64
}

// Defaults.
const (
	DefaultHostTTL  = 30 * time.Second
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
	source  project.Registry
	now     func() time.Time
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
func WithSource(s project.Registry) Option {
	return func(o *options) error {
		if s == nil {
			return ErrNoSource
		}
		o.source = s
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
	if c.HostTTL == 0 {
		c.HostTTL = DefaultHostTTL
	}
	if c.MaxHosts == 0 {
		c.MaxHosts = DefaultMaxHosts
	}
	if c.Timeout == 0 {
		c.Timeout = DefaultTimeout
	}
	if c.MaxBody == 0 {
		c.MaxBody = DefaultMaxBody
	}
	switch {
	case c.HostTTL < 0:
		return Config{}, fmt.Errorf("gateway: host ttl %s is not a duration to remember for", c.HostTTL)
	case c.MaxHosts < 1:
		return Config{}, fmt.Errorf("gateway: %d hosts: a table that holds nothing asks on every request", c.MaxHosts)
	case c.Timeout <= 0:
		return Config{}, fmt.Errorf("gateway: timeout %s: a request needs a budget", c.Timeout)
	case c.MaxBody < 1:
		return Config{}, fmt.Errorf("gateway: %d bytes: a body limit of nothing refuses every request with a body", c.MaxBody)
	}
	return c, nil
}
