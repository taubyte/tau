package config

import (
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
	Privatekey  string
	Swarmkey    string
	Protocols   []string            `yaml:",omitempty"`
	P2PListen   []string            `yaml:"p2p-listen"`
	P2PAnnounce []string            `yaml:"p2p-announce"`
	Ports       Ports               `yaml:"ports"`
	Location    *seerIface.Location `yaml:"location,omitempty"`
	Peers       []string            `yaml:",omitempty"`
	NetworkFqdn string              `yaml:"network-fqdn"`
	Domains     Domains             `yaml:"domains"`
	Plugins
}

type Plugin string

type Plugins struct {
	Plugins []Plugin `yaml:",omitempty"`
}

type Domains struct {
	Key       DVKey            `yaml:"key"`
	Whitelist DomainsWhiteList `yaml:"whitelist"`
	Services  string           `yaml:"services"`
	Generated string           `yaml:"generated"`
}

type DomainsWhiteList struct {
	Postfix []string
	Regex   []string
}

type DVKey struct {
	Private string `yaml:"private"`
	Public  string `yaml:"public"`
}
