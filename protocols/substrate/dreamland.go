package substrate

import (
	"context"
	"fmt"

	iface "github.com/taubyte/go-interfaces/common"
	tauConfig "github.com/taubyte/tau/config"
	dreamlandCommon "github.com/taubyte/tau/libdream/common"
	dreamlandRegistry "github.com/taubyte/tau/libdream/registry"
	"github.com/taubyte/tau/protocols/substrate/mocks/counters"
)

func init() {
	dreamlandRegistry.Registry.Substrate.Service = createNodeService
}

func createNodeService(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
	serviceConfig := &tauConfig.Node{
		Ports: make(map[string]int),
	}
	serviceConfig.Root = config.Root
	serviceConfig.P2PListen = []string{fmt.Sprintf(dreamlandCommon.DefaultP2PListenFormat, config.Port)}
	serviceConfig.P2PAnnounce = []string{fmt.Sprintf(dreamlandCommon.DefaultP2PListenFormat, config.Port)}
	serviceConfig.DevMode = true
	serviceConfig.SwarmKey = config.SwarmKey
	serviceConfig.Databases = config.Databases

	serviceConfig.HttpListen = fmt.Sprintf("%s:%d", dreamlandCommon.DefaultHost, config.Others["http"])

	serviceConfig.Ports["ipfs"] = config.Others["ipfs"]

	if config.Others["verbose"] != 0 {
		serviceConfig.Verbose = true
	}

	if result, ok := config.Others["secure"]; ok {
		serviceConfig.EnableHTTPS = (result != 0)
	}

	serviceConfig.DomainValidation.PublicKey = config.PublicKey
	service, err := New(ctx, serviceConfig)
	if err != nil {
		return nil, err
	}

	service.nodeCounters = counters.New(service)

	return service, nil
}
