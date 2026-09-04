package node

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/libp2p/go-libp2p/core/host"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/services/build"
	"github.com/taubyte/tau/services/build/backend"
	"github.com/taubyte/tau/services/build/frontend"
	"github.com/taubyte/tau/services/stats"
	auth "github.com/taubyte/tau/services/v2/auth"
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
	Auth     = "auth"
)

// The two halves of the build service, named by the build service itself
// rather than spelled again here — a rename there would otherwise leave an
// operator's config naming a service nothing starts.
//
// THEY ARE TWO NAMES BECAUSE THEY MUST BE TWO PROCESSES.
// services/build/boundary_test.go holds the import half of that: the public
// role may not import the private one, so the process a stranger's POST
// reaches does not hold the queue's writer or a repository's deploy key. One
// node running both would put all of it there and reduce the split to a
// directory layout, so withDefaults refuses the combination.
var (
	// BuildFrontend is the webhook a provider posts to and the job API a
	// developer reads. It is the only public role in this composition.
	BuildFrontend = build.Frontend.Name
	// BuildBackend holds the queue, the job records, the clone credentials and
	// the runners.
	BuildBackend = build.Backend.Name
)

// All is every service this composition knows how to start.
//
// dns AND proxy ARE NOT HERE YET. dns answers a query and has no listener of
// its own — it is a function from a question to an answer, and something has to
// bind :53 and call it. proxy is an http.Handler that needs an L4 listener and
// an allocated address per resource. Both are real services; neither is a line
// in this file, and listing them before they can be started would make an
// operator's config lie.
var All = []string{Register, Worker, Stats, Gateway, Auth, BuildBackend}

// BuildFrontend is NOT in All. All is what a single-node development cloud
// starts, and that node runs the backend; putting the public half in the
// default shape would make the default the one combination this file refuses.

// Config is what an operator declared about this node, and ONLY that.
type Config struct {
	// Services this node runs. Empty means all of them, which is the
	// single-node development shape.
	Services []string

	// Cloud scopes what this node gossips with. Two clouds sharing a machine,
	// or bridged by a stray peer, must not pool their load figures and route
	// each other's traffic.
	Cloud string

	// Container is the container tier this node runs, if any. See
	// container.go, and in particular what its Runtime field is for.
	Container ContainerConfig

	// Per-service operator configuration. Each is that service's own; nothing
	// here reinterprets it.
	Stats   stats.Config
	Worker  worker.Config
	Gateway gateway.Config
	Auth    auth.Config

	// BuildFrontend and BuildBackend are the two halves' own configuration.
	// A node uses at most one of them.
	BuildFrontend frontend.Config
	BuildBackend  backend.Config
}

var (
	ErrNoHost      = errors.New("node: no host to answer on")
	ErrNoDatabases = errors.New("node: a register needs somewhere to keep what is published")
	ErrNoPubSub    = errors.New("node: stats needs somewhere to gossip")
	ErrNoRaft      = errors.New("node: the build backend's queue is replicated through raft, and nothing here opens one")

	// ErrBothHalves is the one service combination this composition refuses.
	ErrBothHalves = errors.New("node: " + build.Frontend.Name + " and " + build.Backend.Name +
		" must not run in one process; the public half exists so a stranger's POST does not reach the queue or the clone credentials")
)

// Option injects what a node is built from.
type Option func(*options) error

type options struct {
	host      host.Host
	pubsub    stats.PubSub
	databases kvdb.Factory
	raft      func(namespace string) (raft.Cluster, error)
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

// WithRaft is how the build backend opens the replicated log its queue and job
// records live in. Required when this node runs that role.
//
// IT IS A FUNCTION OF A NAMESPACE, injected for the same reason the host is:
// raft.New takes a peer.Node — a host, a DHT, a datastore and a lifetime —
// which is one machine's resource. A composition that built its own could not
// put two nodes in one process, which is exactly what dream does.
func WithRaft(open func(namespace string) (raft.Cluster, error)) Option {
	return func(o *options) error {
		if open == nil {
			return ErrNoRaft
		}
		o.raft = open
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
	known := append(append([]string(nil), All...), BuildFrontend)
	for _, name := range c.Services {
		if !slices.Contains(known, name) {
			return Config{}, fmt.Errorf("node: %q is not a service this node can run; it runs %v", name, known)
		}
	}
	if slices.Contains(c.Services, BuildFrontend) && slices.Contains(c.Services, BuildBackend) {
		return Config{}, ErrBothHalves
	}
	return c, nil
}

func (c Config) runs(name string) bool { return slices.Contains(c.Services, name) }
