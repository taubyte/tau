package config

import (
	seerIface "github.com/taubyte/go-interfaces/services/seer"
)

type Source struct {
	Privatekey  string
	Swarmkey    string
	Protocols   []string `yaml:",omitempty"`
	P2PListen   []string `yaml:"p2p-listen"`
	P2PAnnounce []string `yaml:"p2p-announce"`
	Ports       map[string]int
	Location    *seerIface.Location `yaml:"location,omitempty"`
	Peers       []string            `yaml:",omitempty"`
	HttpListen  string              `yaml:"http-listen,omitempty"`
	NetworkUrl  string              `yaml:"network-url"`
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
