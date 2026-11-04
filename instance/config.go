package instance

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/taubyte/tau/pkg/containers"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/drivers"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/pkg/tenancy"
	"github.com/taubyte/tau/services"
	auth "github.com/taubyte/tau/services/auth"
	"github.com/taubyte/tau/services/build"
	"github.com/taubyte/tau/services/build/backend"
	"github.com/taubyte/tau/services/build/frontend"
	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/services/dns"
	gateway "github.com/taubyte/tau/services/gateway"
	"github.com/taubyte/tau/services/primitives"
	proxy "github.com/taubyte/tau/services/proxy"
	"github.com/taubyte/tau/services/worker"
	"gopkg.in/yaml.v3"
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
var All = []string{Worker, Gateway, Auth, Proxy, DNS, BuildBackend}

// Everything is every service a node can be configured to run: the ones this
// package still starts itself and the ones services.New makes. All, the default
// shape, is the first half of it.
func Everything() []string {
	return append(append(append([]string(nil), All...), BuildFrontend, CM), services.Names()...)
}

// BuildFrontend is NOT in All. All is what a single-node development cloud
// starts, and that node runs the backend; putting the public half in the
// default shape would make the default the one combination this file refuses.

// Config is what an operator declared about this node, and ONLY that.
type Config struct {
	// Services this node runs. Empty means all of them, which is the
	// single-node development shape.
	Services []string

	// Root is where this instance keeps what has to be a real directory: its raft
	// snapshots, a secrets share, volumes, the SQL engine's files. Empty, what
	// needs one is refused by name.
	Root string

	// Cloud scopes what this node gossips with. Two clouds sharing a machine,
	// or bridged by a stray peer, must not pool their load figures and route
	// each other's traffic.
	Cloud string

	// Region is the region this node serves: every project it serves is read as
	// that region's view of it, and nothing placed elsewhere reaches it. Empty is
	// schema.DefaultRegion, the one destination of a build with no regions.
	Region string

	// Tenancy are the forge namespaces this cloud serves, each written
	// host/namespace ("github.com/acme"). A repository outside them is
	// refused by name wherever one is registered, linked to a project or
	// built; empty serves whatever the forges grant. Entries are
	// canonicalised (lowercased) at construction. See pkg/tenancy.
	Tenancy []string

	// Forges are the git hosts this cloud serves, and the driver for each.
	// Empty means drivers.Default. See pkg/forge/drivers.
	Forges []drivers.Forge

	// Container is the container tier this node runs, if any. See
	// container.go, and in particular what its Runtime field is for.
	Container containers.Declared

	// Per-service operator configuration. Each is that service's own; nothing
	// here reinterprets it.
	Worker  worker.Config
	Gateway gateway.Config

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

	// Sections is each service's own configuration, under its name, at the top
	// level of the file beside the instance's own keys. Nothing here reads it: the
	// service decodes its own with Host.Config. A section naming no service is
	// refused, for the reason a misspelled service name is.
	Sections map[string]any `yaml:",inline"`
}

var (
	ErrNoPeer = errors.New("instance: no peer to be made from")

	// ErrBothHalves is the one service combination this composition refuses.
	ErrBothHalves = errors.New("node: " + build.Frontend.Name + " and " + build.Backend.Name +
		" must not run in one process; the public half exists so a stranger's POST does not reach the queue or the clone credentials")
)

type Option func(*options) error

type options struct {
	// kvdbOpen and raftOpen stand in for the peer's own, for a test that has to
	// see what was opened or make an open fail. Nothing outside this package's
	// tests can set them.
	kvdbOpen func(name string) (kvdb.Instance, error)
	raftOpen func(namespace string) (raft.Cluster, error)

	engines   *database.Registry
	runtimes  []invoke.Runtime
	artifacts worker.Artifacts
	secrets   worker.Secrets
	now       func() time.Time
	extra     map[string]service.Service
	identity  func(Host) (auth.Identity, error)
	regions   schema.Regions
	resolver  cm.Resolver
	forges    *forge.Registry

	// terminates says whatever serves Handler serves it over TLS too, with
	// TLSConfig. See WithTLS.
	terminates bool
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

// withDefaults fills and checks the service list. added are the names of the services
// tau ships beside the built-ins: an empty list runs them, and a list may name them.
func (c Config) withDefaults(added ...string) (Config, error) {
	if len(c.Services) == 0 {
		c.Services = append(append([]string(nil), All...), added...)
	}
	// WHAT A SERVICE CANNOT START WITHOUT RUNS BESIDE IT, so a list naming the
	// service alone still starts.
	for _, name := range slices.Clone(c.Services) {
		for _, need := range services.Needs(name) {
			if !slices.Contains(c.Services, need) {
				c.Services = append(c.Services, need)
			}
		}
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
	for name := range c.Sections {
		if !slices.Contains(added, name) {
			return Config{}, fmt.Errorf("node: there is configuration for %q, which is not a service this node can run; it runs %v", name, added)
		}
	}
	return c, nil
}

func (c Config) runs(name string) bool { return slices.Contains(c.Services, name) }

// Section decodes the named service's section into into, by yaml. With no section it
// leaves into as it was, so whatever into held is the default.
//
// A ROUND TRIP AND NOT A CAST. From a file a section is whatever yaml decoded it
// to; from Go it may be the service's own struct. Either way the service gets what
// its yaml tags say, exactly as an operator wrote it.
func (c Config) Section(name string, into any) error {
	section, ok := c.Sections[name]
	if !ok {
		return nil
	}
	raw, err := yaml.Marshal(section)
	if err != nil {
		return fmt.Errorf("node: %s: config: %w", name, err)
	}
	if err := yaml.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("node: %s: config: %w", name, err)
	}
	return nil
}
