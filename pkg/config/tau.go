package config

import (
	"time"

	seerIface "github.com/taubyte/tau/core/services/seer"
	"gopkg.in/yaml.v3"
)

type Ports struct {
	Main int `yaml:"main"`
	Lite int `yaml:"lite,omitempty"`
	Ipfs int `yaml:"ipfs,omitempty"`
}

func (p Ports) ToMap() map[string]int {
	return map[string]int{
		"main": p.Main,
		"lite": p.Lite,
		"ipfs": p.Ipfs,
	}
}

type Source struct {
	Privatekey  string              `yaml:"privatekey"`
	Swarmkey    string              `yaml:"swarmkey"`
	Services    []string            `yaml:"services,omitempty"`
	P2PListen   []string            `yaml:"p2p-listen"`
	P2PAnnounce []string            `yaml:"p2p-announce"`
	Ports       Ports               `yaml:"ports"`
	Location    *seerIface.Location `yaml:"location,omitempty"`
	Peers       []string            `yaml:"peers,omitempty"`
	NetworkFqdn string              `yaml:"network-fqdn"`
	Domains     Domains             `yaml:"domains"`
	Cluster     string              `yaml:"cluster,omitempty"`
	// Accounts subsystem config (session-ttl, email/SMTP). Optional —
	// community / dream installs can omit. AccountsURL + WebAuthn are
	// derived from NetworkFqdn at runtime.
	Accounts Accounts `yaml:"accounts,omitempty"`
	// Tenancy names the git namespace that owns this cloud. Unlike Accounts it
	// is emitted to every shape, so any service can compare a repository's
	// owner against it without a client or an API call.
	Tenancy   Tenancy   `yaml:"tenancy,omitempty"`
	Substrate Substrate `yaml:"substrate,omitempty"`
	Quotas    Quotas    `yaml:"quotas,omitempty"`
	// Enterprise namespaces raw config for enterprise-only services under
	// `enterprise:` in the shape config. Community builds carry it opaquely;
	// `//go:build ee` code decodes each service's entry into its own typed
	// config (via EnterpriseConfig).
	Enterprise map[string]yaml.Node `yaml:"enterprise,omitempty"`
	Plugins
}

// Caps are per-project limits an operator sets; 0 means unlimited.
type Caps struct {
	StorageBytes   uint64 `yaml:"storage-bytes,omitempty"` // total size of a project's databases and storages
	Resources      uint64 `yaml:"resources,omitempty"`     // number of databases and storages
	FunctionMemory uint64 `yaml:"function-memory,omitempty"`
	Instances      int    `yaml:"instances,omitempty"`
	Builds         int    `yaml:"builds,omitempty"`
}

// Quotas holds the default caps and per-project overrides.
type Quotas struct {
	Default  Caps            `yaml:"default,omitempty"`
	Projects map[string]Caps `yaml:"projects,omitempty"`
}

// For returns a project's caps: each non-zero override field wins over the default.
func (q Quotas) For(projectID string) Caps {
	c, o := q.Default, q.Projects[projectID]
	if o.StorageBytes != 0 {
		c.StorageBytes = o.StorageBytes
	}
	if o.Resources != 0 {
		c.Resources = o.Resources
	}
	if o.FunctionMemory != 0 {
		c.FunctionMemory = o.FunctionMemory
	}
	if o.Instances != 0 {
		c.Instances = o.Instances
	}
	if o.Builds != 0 {
		c.Builds = o.Builds
	}
	return c
}

// Substrate holds substrate-only settings.
type Substrate struct {
	// Migration runs the node-local data migration at boot (default on);
	// `substrate: {migration: false}` turns it off.
	Migration *bool `yaml:"migration,omitempty"`
}

// MigrationEnabled reports whether the node-local data migration runs.
func (s Substrate) MigrationEnabled() bool { return s.Migration == nil || *s.Migration }

type BundleOrigin struct {
	Shape     string    `yaml:"shape"`
	Host      string    `yaml:"host"`
	Creation  time.Time `yaml:"time"`
	Version   *string   `yaml:"version,omitempty"`
	Protected bool      `yaml:"protected,omitempty"`
}

type Bundle struct {
	Origin BundleOrigin `yaml:"origin"`
	Source
}

type Plugin string

type Plugins struct {
	Plugins []Plugin `yaml:"plugins,omitempty"`
}

type Domains struct {
	Key       DVKey       `yaml:"key"`
	Acme      *AcmeConfig `yaml:"acme,omitempty"`
	Aliases   []string    `yaml:"aliases"`
	Generated string      `yaml:"generated"`
	// Hosts binds a custom domain to the service that serves it, e.g.
	// {"console.example.com": "gateway"}. seer resolves the domain to that service's
	// nodes, the shared http server accepts + autocerts it, and the service
	// registers its routes under it. Distinct from Aliases (generic
	// <svc>.<alias> base domains resolved by first label).
	Hosts map[string]string `yaml:"hosts,omitempty"`
}

type AcmeCAConfig struct {
	RootCA     string `yaml:"root-ca"`
	CAARecord  string `yaml:"caa-record"`
	SkipVerify bool   `yaml:"skip"`
}

type AcmeConfig struct {
	Url string        `yaml:"url"`
	CA  *AcmeCAConfig `yaml:"ca,omitempty"`
	Key string        `yaml:"key"`
}

type DVKey struct {
	Private string `yaml:"private"`
	Public  string `yaml:"public"`
}
