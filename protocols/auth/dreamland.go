package auth

import (
	"context"
	"fmt"

	iface "github.com/taubyte/go-interfaces/common"
	commonSpecs "github.com/taubyte/go-specs/common"
	tauConfig "github.com/taubyte/tau/config"
	"github.com/taubyte/tau/libdream"
)

func init() {
	if err := libdream.Registry.Set(commonSpecs.Auth, createAuthService, nil); err != nil {
		panic(err)
	}
}

func createAuthService(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
	serviceConfig := &tauConfig.Node{}
	serviceConfig.Root = config.Root
	serviceConfig.P2PListen = []string{fmt.Sprintf(libdream.DefaultP2PListenFormat, config.Port)}
	serviceConfig.P2PAnnounce = []string{fmt.Sprintf(libdream.DefaultP2PListenFormat, config.Port)}
	serviceConfig.DevMode = true
	serviceConfig.SwarmKey = config.SwarmKey

	serviceConfig.HttpListen = fmt.Sprintf("%s:%d", libdream.DefaultHost, config.Others["http"])

	if result, ok := config.Others["secure"]; ok {
		serviceConfig.EnableHTTPS = (result != 0)
	}

	serviceConfig.Databases = config.Databases

	serviceConfig.DomainValidation.PrivateKey = config.PrivateKey
	serviceConfig.DomainValidation.PublicKey = config.PublicKey

	return New(ctx, serviceConfig)
}
