package config

import (
	"github.com/taubyte/go-interfaces/services/common"
	seerIface "github.com/taubyte/go-interfaces/services/seer"
)

type Odo struct {
	Privatekey  string
	Swarmkey    string
	Protocols   []string `yaml:",omitempty"`
	P2PListen   []string `yaml:"p2p-listen"`
	P2PAnnounce []string `yaml:"p2p-announce"`
	Ports       map[string]int
	Location    *seerIface.Location   `yaml:"location,omitempty"`
	Peers       []string              `yaml:",omitempty"`
	HttpListen  string                `yaml:"http-listen,omitempty"`
	NetworkUrl  string                `yaml:"network-url"`
	Domains     common.HttpDomainInfo `yaml:"domains"`
	Plugins     []string              `yaml:",omitempty"`
}
