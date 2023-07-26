package service

import (
	"context"
	"fmt"

	dreamlandCommon "github.com/taubyte/dreamland/core/common"
	dreamlandRegistry "github.com/taubyte/dreamland/core/registry"
	iface "github.com/taubyte/go-interfaces/common"
	odoConfig "github.com/taubyte/odo/config"
	protocolsCommon "github.com/taubyte/odo/protocols/common"
)

func init() {
	dreamlandRegistry.Registry.Patrick.Service = createPatrickService
}

func createPatrickService(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
	serviceConfig := &odoConfig.Protocol{}
	serviceConfig.Root = config.Root
	serviceConfig.P2PListen = []string{fmt.Sprintf(dreamlandCommon.DefaultP2PListenFormat, config.Port)}
	serviceConfig.P2PAnnounce = []string{fmt.Sprintf(dreamlandCommon.DefaultP2PListenFormat, config.Port)}
	serviceConfig.DevMode = true
	serviceConfig.SwarmKey = config.SwarmKey

	if config.Others["http"] != 443 {
		serviceConfig.HttpListen = fmt.Sprintf("%s:%d", dreamlandCommon.DefaultHost, config.Others["http"])
	}

	// Used to test cancel job on go-patrick-http
	if result, ok := config.Others["delay"]; ok {
		if result == 1 {
			protocolsCommon.DelayJob = true
		}
	}

	// Used to test retry job on go-patrick-http
	if result, ok := config.Others["retry"]; ok {
		if result == 1 {
			protocolsCommon.RetryJob = true
		}
	}

	if result, ok := config.Others["secure"]; ok {
		serviceConfig.EnableHTTPS = (result != 0)
	}

	return New(ctx, serviceConfig)
}
