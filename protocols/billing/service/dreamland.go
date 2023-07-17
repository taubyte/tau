package service

import (
	"context"
	"fmt"

	"bitbucket.org/taubyte/dreamland/common"
	dreamlandCommon "bitbucket.org/taubyte/dreamland/common"
	dreamlandRegistry "bitbucket.org/taubyte/dreamland/registry"
	iface "github.com/taubyte/go-interfaces/common"
	commoniface "github.com/taubyte/go-interfaces/services/common"
)

func init() {
	dreamlandRegistry.Registry.Billing.Service = createBillingService
}

func createBillingService(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
	serviceConfig := &commoniface.GenericConfig{}
	serviceConfig.Root = config.Root
	serviceConfig.P2PListen = []string{fmt.Sprintf(common.DefaultP2PListenFormat, config.Port)}
	serviceConfig.P2PAnnounce = []string{fmt.Sprintf(common.DefaultP2PListenFormat, config.Port)}
	serviceConfig.Bootstrap = false
	serviceConfig.DevMode = true
	serviceConfig.SwarmKey = config.SwarmKey

	if config.Others["http"] != 443 {
		serviceConfig.HttpListen = fmt.Sprintf("%s:%d", dreamlandCommon.DefaultURL, config.Others["http"])
	}

	if result, ok := config.Others["secure"]; ok == true {
		serviceConfig.HttpSecure = (result != 0)
	}

	return New(ctx, serviceConfig)
}
