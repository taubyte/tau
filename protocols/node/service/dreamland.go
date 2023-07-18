package service

import (
	"context"
	"fmt"

	dreamlandCommon "github.com/taubyte/dreamland/core/common"
	dreamlandRegistry "github.com/taubyte/dreamland/core/registry"
	iface "github.com/taubyte/go-interfaces/common"
	commonIface "github.com/taubyte/go-interfaces/services/common"
)

func init() {
	dreamlandRegistry.Registry.Node.Service = createNodeService
}

func createNodeService(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
	serviceConfig := &commonIface.GenericConfig{
		Ports: make(map[string]int),
	}
	serviceConfig.Root = config.Root
	serviceConfig.P2PListen = []string{fmt.Sprintf(dreamlandCommon.DefaultP2PListenFormat, config.Port)}
	serviceConfig.P2PAnnounce = []string{fmt.Sprintf(dreamlandCommon.DefaultP2PListenFormat, config.Port)}
	serviceConfig.Bootstrap = false
	serviceConfig.DevMode = true
	serviceConfig.SwarmKey = config.SwarmKey

	if config.Others["http"] != 443 {
		serviceConfig.HttpListen = fmt.Sprintf("%s:%d", dreamlandCommon.DefaultURL, config.Others["http"])
	}

	serviceConfig.Ports["ipfs"] = config.Others["ipfs"]

	if config.Others["verbose"] != 0 {
		serviceConfig.Verbose = true
	}

	if result, ok := config.Others["secure"]; ok {
		serviceConfig.HttpSecure = (result != 0)
	}

	serviceConfig.DVPublicKey = config.PublicKey

	return New(ctx, serviceConfig)
}
