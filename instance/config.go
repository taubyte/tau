package instance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/libp2p/go-libp2p/core/host"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/pkg/tenancy"
	auth "github.com/taubyte/tau/services/auth"
	"github.com/taubyte/tau/services/build"
	"github.com/taubyte/tau/services/build/artifact"
	"github.com/taubyte/tau/services/build/backend"
	"github.com/taubyte/tau/services/build/deploy"
	"github.com/taubyte/tau/services/build/frontend"
	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/services/discovery"
	"github.com/taubyte/tau/services/dns"
	gateway "github.com/taubyte/tau/services/gateway"
	"github.com/taubyte/tau/services/primitives"
	proxy "github.com/taubyte/tau/services/proxy"
	"github.com/taubyte/tau/services/secrets"
	"github.com/taubyte/tau/services/worker"
)

// Services a node can run. They are named here so an operator's list is checked
// at construction against a closed set rather than silently ignored — a
// misspelled service that starts nothing is a node that looks healthy and
// answers nothing.
const (
	Register  = "register"
	Worker    = "worker"
	Discovery = "discovery"
	Gateway   = "gateway"
	Auth      = "auth"
	Secrets   = "secrets"
	Proxy     = "proxy"
	DNS       = "dns"

	// Database holds a project's databases. It is its OWN service and not the
	// worker's job — v1 made the substrate open them, so the data plane was
	// every node that had ever run code. See services/database.
	Database = "database"
)

// Primitives holds a project's `sync` kind (queues and logs), borrowed from the
// service's descriptor so a rename cannot leave config naming nothing.
var Primitives = primitives.Descriptor.Name

// The two halves of the build service, named by the build service itself. Two
// processes: the public role may not import the private one
// (services/build/boundary_test.go), and withDefaults refuses one node running both.
var (
	// BuildFrontend is the webhook a provider posts to and the job API a
	// developer reads. It is the only public role in this composition.
	BuildFrontend = build.Frontend.Name
	// BuildBackend holds the queue, the job records, the clone credentials and
	// the runners.
	BuildBackend = build.Backend.Name
)

// All is every service this composition knows how to start.
var All = []string{Register, Worker, Discovery, Gateway, Auth, Secrets, Database, Primitives, Proxy, DNS, BuildBackend}

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

	// Tenancy are the forge namespaces this cloud serves, each written
	// host/namespace ("github.com/acme"). A repository outside them is
	// refused by name wherever one is registered, linked to a project or
	// built; empty serves whatever the forges grant. Entries are
	// canonicalised (lowercased) at construction. See pkg/tenancy.
	Tenancy []string

	// Forges are the git hosts this cloud serves, and the driver for each.
	// Empty means DefaultForges. See forge.go.
	Forges []Forge

	// Container is the container tier this node runs, if any. See
	// container.go, and in particular what its Runtime field is for.
	Container ContainerConfig

	// Per-service operator configuration. Each is that service's own; nothing
	// here reinterprets it.
	Discovery discovery.Config
	Worker    worker.Config
	Gateway   gateway.Config
	Secrets   secrets.Config

	// CM is what the cloud keeps once for every region: today its
	// certificates — the ACME CA, the challenge type and whether the zone's
	// wildcard is issued. Every node of a cloud that issues carries it,
	// because every node orders and serves its own certificates. See services/cm.
	CM    cm.Config
	Proxy proxy.Config
	// Auth names the built-in admin, who may sign up, the console callbacks
	// the forges people sign in through and the OIDC providers.
	Auth auth.Config
	DNS  dns.Config

	// DNSListen is the port this node's dns server answers on, e.g. ":53" or ":5353".
	// Default DefaultDNSListen. The operator's, since 53 is the one port a resolver dials
	// by convention.
	DNSListen string

	// BuildFrontend and BuildBackend are the two halves' own configuration.
	// A node uses at most one of them.
	BuildFrontend frontend.Config
	BuildBackend  backend.Config

	// Added is the configuration of each service added with WithService, under
	// the name it was added by. Nothing here reads it: the service decodes its
	// own entry with Host.Config. An entry naming no added service is refused,
	// for the reason a misspelled service name is.
	Added map[string]any
}

var (
	ErrNoHost      = errors.New("node: no host to answer on")
	ErrNoDatabases = errors.New("node: a register needs somewhere to keep what is published")
	ErrNoPubSub    = errors.New("node: discovery needs somewhere to gossip")
	// ErrNoRaft is a node that needs consensus and was given none: the build backend's
	// queue, the primitives service, a project's queue, or a raft database. It names none
	// of them, since the fix is the same: a composition that opens a group.
	ErrNoRaft = errors.New("node: this needs raft consensus, and nothing here opens a group")

	// ErrBothHalves is the one service combination this composition refuses.
	ErrBothHalves = errors.New("node: " + build.Frontend.Name + " and " + build.Backend.Name +
		" must not run in one process; the public half exists so a stranger's POST does not reach the queue or the clone credentials")
)

type Option func(*options) error

type options struct {
	host      host.Host
	pubsub    discovery.PubSub
	databases kvdb.Factory
	engines   *database.Registry
	raft      func(namespace string) (raft.Cluster, error)
	assets    Assets
	root      string
	runtimes  []invoke.Runtime
	artifacts worker.Artifacts
	secrets   worker.Secrets
	now       func() time.Time
	extra     []Service
	identity  func(Host) (auth.Identity, error)
	regions   schema.Regions
	resolver  cm.Resolver
	forges    *forge.Registry
	// buildSeams and uploads are a development cloud's; see WithBuildSeams.
	buildSeams func(BuildSeams) BuildSeams
	uploads    frontend.Uploads
	// destination is the region whose views this node reads; see AsDestination.
	destination string

	// terminates says whatever serves Handler serves it over TLS too, with
	// TLSConfig. See WithTLS.
	terminates bool
}

// WithHost is the libp2p host every p2p service answers on. Required; a node never
// mints its own identity.
func WithHost(h host.Host) Option {
	return func(o *options) error {
		if h == nil {
			return ErrNoHost
		}
		o.host = h
		return nil
	}
}

// WithPubSub is where discovery gossips. Required when this node runs discovery.
func WithPubSub(ps discovery.PubSub) Option {
	return func(o *options) error {
		if ps == nil {
			return ErrNoPubSub
		}
		o.pubsub = ps
		return nil
	}
}

// WithDatabaseEngines is what a workload's `database:` access is served from: a
// registry, since a deployment imports and registers the drivers it wants and this
// repository links none.
func WithDatabaseEngines(r *database.Registry) Option {
	return func(o *options) error {
		if r == nil {
			return errors.New("node: a nil database registry")
		}
		o.engines = r
		return nil
	}
}

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

// Assets is the byte store this node writes builds into and reads sites out of.
//
// IT IS BOTH HALVES BECAUSE THIS NODE IS BOTH. The build backend only puts
// (backend.Assets) and the static tier only gets (static.Assets); the
// composition that has one has the other, and declaring the pair here is what
// lets a single store be handed to both instead of the same object arriving
// through two options.
type Assets interface {
	Put(ctx context.Context, content io.Reader) (string, error)
	Get(ctx context.Context, id string) (io.ReadSeekCloser, error)
}

// WithAssets is where a build's output goes and a built site is read from. Optional:
// without it a backend still builds, and logs and artifacts live and die with the
// run.
func WithAssets(a Assets) Option {
	return func(o *options) error {
		if a == nil {
			return errors.New("node: a nil asset store")
		}
		o.assets = a
		return nil
	}
}

// WithRoot is where this node keeps what has to be a real directory: volumes, the one
// POSIX filesystem a container mounts. Without it a container declaring a volume is
// refused rather than started on an empty layer.
func WithRoot(dir string) Option {
	return func(o *options) error {
		if dir == "" {
			return errors.New("node: an empty root is not a directory")
		}
		o.root = dir
		return nil
	}
}

// WithTLS says the composer serves this node's gateway over TLS as well, answering
// with TLSConfig. The composer binds the sockets, so it says.
func WithTLS() Option {
	return func(o *options) error {
		o.terminates = true
		return nil
	}
}

// WithRuntimes are the compute tiers this node can run. Required with a worker. A tier
// is a live object, a property of the machine.
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

// BuildSeams are what a build backend reads repositories with and runs builds
// in: the clone a push is confirmed against and checked out from, and the
// engine a code build runs in — nil on a node that runs no containers.
type BuildSeams struct {
	Checkout deploy.Checkout
	Confirm  backend.Confirmer
	Engine   artifact.Engine
}

// WithBuildSeams puts a development cloud's seams in front of the node's (an upload
// read as a commit, and run). For instance/dream only.
func WithBuildSeams(layer func(BuildSeams) BuildSeams) Option {
	return func(o *options) error {
		if layer == nil {
			return errors.New("node: nil build seams")
		}
		o.buildSeams = layer
		return nil
	}
}

// WithUploads gives the build frontend a universe's door for a developer's build
// (frontend.Uploads). For instance/dream only.
func WithUploads(u frontend.Uploads) Option {
	return func(o *options) error {
		if u == nil {
			return errors.New("node: nil uploads")
		}
		o.uploads = u
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

func WithClock(now func() time.Time) Option {
	return func(o *options) error {
		if now == nil {
			return errors.New("node: a nil clock")
		}
		o.now = now
		return nil
	}
}

// withDefaults fills and checks the service list. added are the names of services
// added with WithService: an empty list runs them, and a list may name them.
func (c Config) withDefaults(added ...string) (Config, error) {
	if len(c.Services) == 0 {
		c.Services = append(append([]string(nil), All...), added...)
	}
	known := append(builtIn(), added...)
	for _, name := range c.Services {
		if !slices.Contains(known, name) {
			return Config{}, fmt.Errorf("node: %q is not a service this node can run; it runs %v", name, known)
		}
	}
	if slices.Contains(c.Services, BuildFrontend) && slices.Contains(c.Services, BuildBackend) {
		return Config{}, ErrBothHalves
	}
	served, err := tenancy.Parse(c.Tenancy)
	if err != nil {
		return Config{}, fmt.Errorf("node: %w", err)
	}
	c.Tenancy = served.Namespaces()
	for name := range c.Added {
		if !slices.Contains(added, name) {
			return Config{}, fmt.Errorf("node: there is configuration for %q, which is not a service added to this node; it adds %v", name, added)
		}
	}
	return c, nil
}

func (c Config) runs(name string) bool { return slices.Contains(c.Services, name) }
