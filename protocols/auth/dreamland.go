package auth

import (
	"context"
	"fmt"

	iface "github.com/taubyte/go-interfaces/common"
	odoConfig "github.com/taubyte/tau/config"
	dreamlandCommon "github.com/taubyte/tau/libdream/common"
	dreamlandRegistry "github.com/taubyte/tau/libdream/registry"
)

func init() {
	dreamlandRegistry.Registry.Auth.Service = createAuthService
}

func createAuthService(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
	serviceConfig := &odoConfig.Protocol{}
	serviceConfig.Root = config.Root
	serviceConfig.P2PListen = []string{fmt.Sprintf(dreamlandCommon.DefaultP2PListenFormat, config.Port)}
	serviceConfig.P2PAnnounce = []string{fmt.Sprintf(dreamlandCommon.DefaultP2PListenFormat, config.Port)}
	serviceConfig.DevMode = true
	serviceConfig.SwarmKey = config.SwarmKey

	if config.Others["http"] != 443 {
		serviceConfig.HttpListen = fmt.Sprintf("%s:%d", dreamlandCommon.DefaultHost, config.Others["http"])
	}

	if result, ok := config.Others["secure"]; ok {
		serviceConfig.EnableHTTPS = (result != 0)
	}

	serviceConfig.Databases = config.Databases

	serviceConfig.DomainValidation.PrivateKey = config.PrivateKey
	serviceConfig.DomainValidation.PublicKey = config.PublicKey

	return New(ctx, serviceConfig)
}
