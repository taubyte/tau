package node

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/libp2p/go-libp2p/core/host"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/services/stats"
	gateway "github.com/taubyte/tau/services/v2/gateway"
	"github.com/taubyte/tau/services/worker"
)

// config.go declares what a node is built from.

// Services a node can run. They are named here so an operator's list is checked
// at construction against a closed set rather than silently ignored — a
// misspelled service that starts nothing is a node that looks healthy and
// answers nothing.
const (
	Register = "register"
	Worker   = "worker"
	Stats    = "stats"
	Gateway  = "gateway"
)

// All is every service this composition knows how to start.
//
// dns AND proxy ARE NOT HERE YET. dns answers a query and has no listener of
// its own — it is a function from a question to an answer, and something has to
// bind :53 and call it. proxy is an http.Handler that needs an L4 listener and
// an allocated address per resource. Both are real services; neither is a line
// in this file, and listing them before they can be started would make an
// operator's config lie.
var All = []string{Register, Worker, Stats, Gateway}

// Config is what an operator declared about this node, and ONLY that.
type Config struct {
	// Services this node runs. Empty means all of them, which is the
	// single-node development shape.
	Services []string

	// Cloud scopes what this node gossips with. Two clouds sharing a machine,
	// or bridged by a stray peer, must not pool their load figures and route
	// each other's traffic.
	Cloud string

	// Per-service operator configuration. Each is that service's own; nothing
	// here reinterprets it.
	Stats   stats.Config
	Worker  worker.Config
	Gateway gateway.Config
}

var (
	ErrNoHost      = errors.New("node: no host to answer on")
	ErrNoDatabases = errors.New("node: a register needs somewhere to keep what is published")
	ErrNoPubSub    = errors.New("node: stats needs somewhere to gossip")
)

// Option injects what a node is built from.
type Option func(*options) error

type options struct {
	host      host.Host
	pubsub    stats.PubSub
	databases kvdb.Factory
	runtimes  []invoke.Runtime
	artifacts worker.Artifacts
	secrets   worker.Secrets
	now       func() time.Time
}

// WithHost is the libp2p host every p2p service answers on. Required.
//
// A NODE DOES NOT MINT ITS OWN IDENTITY. A composition that could would have
// two ways to start, one of which is never how production starts it.
func WithHost(h host.Host) Option {
	return func(o *options) error {
		if h == nil {
			return ErrNoHost
		}
		o.host = h
		return nil
	}
}

// WithPubSub is where stats gossips. Required when this node runs stats.
func WithPubSub(ps stats.PubSub) Option {
	return func(o *options) error {
		if ps == nil {
			return ErrNoPubSub
		}
		o.pubsub = ps
		return nil
	}
}

// WithDatabases is where a register keeps what has been published. Required
// when this node runs one.
func WithDatabases(f kvdb.Factory) Option {
	return func(o *options) error {
		if f == nil {
			return ErrNoDatabases
		}
		o.databases = f
		return nil
	}
}

// WithRuntimes are the compute tiers this node can run. Required when it runs a
// worker.
//
// THEY ARE INJECTED BECAUSE A TIER IS A LIVE OBJECT — a connection to a
// container daemon, a wasm engine — and which ones a node has is a property of
// the machine rather than of the configuration.
func WithRuntimes(rts ...invoke.Runtime) Option {
	return func(o *options) error {
		for _, rt := range rts {
			if rt == nil {
				return errors.New("node: a nil runtime")
			}
		}
		o.runtimes = append(o.runtimes, rts...)
		return nil
	}
}

// WithArtifacts resolves what a built resource currently runs. Optional.
func WithArtifacts(a worker.Artifacts) Option {
	return func(o *options) error {
		if a == nil {
			return errors.New("node: a nil artifact resolver")
		}
		o.artifacts = a
		return nil
	}
}

// WithSecrets resolves a secret a run-time variable refers to. Optional.
func WithSecrets(s worker.Secrets) Option {
	return func(o *options) error {
		if s == nil {
			return errors.New("node: a nil secret resolver")
		}
		o.secrets = s
		return nil
	}
}

// WithClock injects the local clock. Tests use it; production does not.
func WithClock(now func() time.Time) Option {
	return func(o *options) error {
		if now == nil {
			return errors.New("node: a nil clock")
		}
		o.now = now
		return nil
	}
}

func (c Config) withDefaults() (Config, error) {
	if len(c.Services) == 0 {
		c.Services = append([]string(nil), All...)
	}
	for _, name := range c.Services {
		if !slices.Contains(All, name) {
			return Config{}, fmt.Errorf("node: %q is not a service this node can run; it runs %v", name, All)
		}
	}
	return c, nil
}

func (c Config) runs(name string) bool { return slices.Contains(c.Services, name) }
