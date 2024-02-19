package common

import (
	"fmt"
	"regexp"

	iface "github.com/taubyte/go-interfaces/common"
	tauConfig "github.com/taubyte/tau/config"
	"github.com/taubyte/tau/libdream"
)

func NewDreamlandConfig(config *iface.ServiceConfig) *tauConfig.Node {
	serviceConfig := &tauConfig.Node{}

	serviceConfig.Ports = make(map[string]int)
	for _, k := range []string{"http", "p2p", "dns", "ipfs"} {
		serviceConfig.Ports[k] = config.Others[k]
	}

	serviceConfig.Root = config.Root
	serviceConfig.P2PListen = []string{fmt.Sprintf(libdream.DefaultP2PListenFormat, config.Port)}
	serviceConfig.P2PAnnounce = []string{fmt.Sprintf(libdream.DefaultP2PListenFormat, config.Port)}
	serviceConfig.DevMode = true
	serviceConfig.SwarmKey = config.SwarmKey

	serviceConfig.HttpListen = fmt.Sprintf("%s:%d", libdream.DefaultHost, config.Others["http"])

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
	serviceConfig.ProtocolsDomainRegExp = regexp.MustCompile(`^([^.]+\.)?tau\.cloud$`)
	serviceConfig.AliasDomainsRegExp = make([]*regexp.Regexp, 0)

	serviceConfig.Databases = config.Databases

	return serviceConfig
}
