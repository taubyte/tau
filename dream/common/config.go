package common

import (
	"fmt"
	"regexp"

	tauConfig "github.com/taubyte/tau/config"
	iface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
)

func NewDreamlandConfig(u *dream.Universe, config *iface.ServiceConfig) *tauConfig.Node {
	serviceConfig := &tauConfig.Node{}

	serviceConfig.Ports = make(map[string]int)
	for _, k := range []string{"http", "p2p", "dns", "ipfs"} {
		serviceConfig.Ports[k] = config.Others[k]
	}

	serviceConfig.Root = config.Root
	serviceConfig.P2PListen = []string{fmt.Sprintf(dream.DefaultP2PListenFormat, config.Port)}
	serviceConfig.P2PAnnounce = []string{fmt.Sprintf(dream.DefaultP2PListenFormat, config.Port)}
	serviceConfig.DevMode = true
	serviceConfig.SwarmKey = config.SwarmKey

	serviceConfig.HttpListen = fmt.Sprintf("%s:%d", dream.DefaultHost, config.Others["http"])

	if config.Others["verbose"] != 0 {
		serviceConfig.Verbose = true
	}

	if result, ok := config.Others["secure"]; ok {
		serviceConfig.EnableHTTPS = (result != 0)
	}

	serviceConfig.Databases = config.Databases

	serviceConfig.DomainValidation.PrivateKey = config.PrivateKey
	serviceConfig.DomainValidation.PublicKey = config.PublicKey

	serviceConfig.NetworkFqdn = "cloud"
	serviceConfig.GeneratedDomainRegExp = regexp.MustCompile(`^[^.]+\.g\.tau\.link$`)
	serviceConfig.ServicesDomainRegExp = regexp.MustCompile(`^([^.]+\.)?tau\.cloud$`)
	serviceConfig.AliasDomainsRegExp = make([]*regexp.Regexp, 0)

	serviceConfig.Databases = config.Databases

	// build bootstrap
	upeers := u.Peers()
	bpeers := make([]string, 0, len(upeers))
	for _, n := range upeers {
		bpeers = append(bpeers, n.Peer().Addrs()[0].String()+"/p2p/"+n.ID().String())
	}
	serviceConfig.Peers = bpeers

	serviceConfig.Location = &config.Location

	return serviceConfig
}
