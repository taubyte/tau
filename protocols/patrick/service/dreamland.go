package service

import (
	"context"
	"fmt"

	dreamlandCommon "bitbucket.org/taubyte/dreamland/common"
	dreamlandRegistry "bitbucket.org/taubyte/dreamland/registry"
	iface "github.com/taubyte/go-interfaces/common"

	commonIface "github.com/taubyte/go-interfaces/services/common"
	patrickCommon "github.com/taubyte/odo/protocols/patrick/common"
)

func init() {
	dreamlandRegistry.Registry.Patrick.Service = createPatrickService
}

func createPatrickService(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
	serviceConfig := &commonIface.GenericConfig{}
	serviceConfig.Root = config.Root
	serviceConfig.P2PListen = []string{fmt.Sprintf(dreamlandCommon.DefaultP2PListenFormat, config.Port)}
	serviceConfig.P2PAnnounce = []string{fmt.Sprintf(dreamlandCommon.DefaultP2PListenFormat, config.Port)}
	serviceConfig.Bootstrap = false
	serviceConfig.DevMode = true
	serviceConfig.SwarmKey = config.SwarmKey

	if config.Others["http"] != 443 {
		serviceConfig.HttpListen = fmt.Sprintf("%s:%d", dreamlandCommon.DefaultURL, config.Others["http"])
	}

	// Used to test cancel job on go-patrick-http
	if result, ok := config.Others["delay"]; ok {
		if result == 1 {
			patrickCommon.DelayJob = true
		}
	}

	// Used to test retry job on go-patrick-http
	if result, ok := config.Others["retry"]; ok {
		if result == 1 {
			patrickCommon.RetryJob = true
		}
	}

	if result, ok := config.Others["secure"]; ok {
		serviceConfig.HttpSecure = (result != 0)
	}

	return New(ctx, serviceConfig)
}
