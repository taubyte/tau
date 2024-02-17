package config

import (
	"time"

	seerIface "github.com/taubyte/go-interfaces/services/seer"
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
	Protocols   []string            `yaml:"protocols,omitempty"`
	P2PListen   []string            `yaml:"p2p-listen"`
	P2PAnnounce []string            `yaml:"p2p-announce"`
	Ports       Ports               `yaml:"ports"`
	Location    *seerIface.Location `yaml:"location,omitempty"`
	Peers       []string            `yaml:"peers,omitempty"`
	NetworkFqdn string              `yaml:"network-fqdn"`
	Domains     Domains             `yaml:"domains"`
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
	Key       DVKey    `yaml:"key"`
	Aliases   []string `yaml:"aliases"`
	Generated string   `yaml:"generated"`
}

type DomainsWhiteList struct {
	Postfix []string
	Regex   []string
}

type DVKey struct {
	Private string `yaml:"private"`
	Public  string `yaml:"public"`
}
