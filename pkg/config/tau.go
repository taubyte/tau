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
	// Enterprise namespaces raw config for enterprise-only services under
	// `enterprise:` in the shape config. Community builds carry it opaquely;
	// `//go:build ee` code decodes each service's entry into its own typed
	// config (via EnterpriseConfig).
	Enterprise map[string]yaml.Node `yaml:"enterprise,omitempty"`
	Plugins
}

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
