package substrate

import (
	"context"
	"fmt"

	iface "github.com/taubyte/go-interfaces/common"
	commonSpecs "github.com/taubyte/go-specs/common"
	tauConfig "github.com/taubyte/tau/config"
	"github.com/taubyte/tau/libdream"
	"github.com/taubyte/tau/protocols/substrate/mocks/counters"
)

func init() {
	if err := libdream.Registry.Set(commonSpecs.Hoarder, createNodeService, nil); err != nil {
		panic(err)
	}
}

func createNodeService(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
	serviceConfig := &tauConfig.Node{
		Ports: make(map[string]int),
	}
	serviceConfig.Root = config.Root
	serviceConfig.P2PListen = []string{fmt.Sprintf(libdream.DefaultP2PListenFormat, config.Port)}
	serviceConfig.P2PAnnounce = []string{fmt.Sprintf(libdream.DefaultP2PListenFormat, config.Port)}
	serviceConfig.DevMode = true
	serviceConfig.SwarmKey = config.SwarmKey
	serviceConfig.Databases = config.Databases

	serviceConfig.HttpListen = fmt.Sprintf("%s:%d", libdream.DefaultHost, config.Others["http"])

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
