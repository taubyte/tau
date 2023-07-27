package substrate

import (
	"context"
	"fmt"

	dreamlandCommon "github.com/taubyte/dreamland/core/common"
	dreamlandRegistry "github.com/taubyte/dreamland/core/registry"
	iface "github.com/taubyte/go-interfaces/common"
	odoConfig "github.com/taubyte/tau/config"
)

func init() {
	dreamlandRegistry.Registry.Substrate.Service = createNodeService
}

func createNodeService(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
	serviceConfig := &odoConfig.Protocol{
		Ports: make(map[string]int),
	}
	serviceConfig.Root = config.Root
	serviceConfig.P2PListen = []string{fmt.Sprintf(dreamlandCommon.DefaultP2PListenFormat, config.Port)}
	serviceConfig.P2PAnnounce = []string{fmt.Sprintf(dreamlandCommon.DefaultP2PListenFormat, config.Port)}
	serviceConfig.DevMode = true
	serviceConfig.SwarmKey = config.SwarmKey
	serviceConfig.Databases = config.Databases

	if config.Others["http"] != 443 {
		serviceConfig.HttpListen = fmt.Sprintf("%s:%d", dreamlandCommon.DefaultHost, config.Others["http"])
	}

	serviceConfig.Ports["ipfs"] = config.Others["ipfs"]

	if config.Others["verbose"] != 0 {
		serviceConfig.Verbose = true
	}

	if result, ok := config.Others["secure"]; ok {
		serviceConfig.EnableHTTPS = (result != 0)
	}

	serviceConfig.DomainValidation.PublicKey = config.PublicKey

	return New(ctx, serviceConfig)
}
