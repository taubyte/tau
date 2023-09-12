package service

import (
	"context"
	"fmt"

	iface "github.com/taubyte/go-interfaces/common"
	commonSpecs "github.com/taubyte/go-specs/common"
	tauConfig "github.com/taubyte/tau/config"
	"github.com/taubyte/tau/libdream"
	protocolsCommon "github.com/taubyte/tau/protocols/common"
)

func init() {
	if err := libdream.Registry.Set(commonSpecs.Patrick, createPatrickService, nil); err != nil {
		panic(err)
	}
}

func createPatrickService(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
	serviceConfig := &tauConfig.Node{}
	serviceConfig.Root = config.Root
	serviceConfig.P2PListen = []string{fmt.Sprintf(libdream.DefaultP2PListenFormat, config.Port)}
	serviceConfig.P2PAnnounce = []string{fmt.Sprintf(libdream.DefaultP2PListenFormat, config.Port)}
	serviceConfig.DevMode = true
	serviceConfig.SwarmKey = config.SwarmKey
	serviceConfig.Databases = config.Databases

	serviceConfig.HttpListen = fmt.Sprintf("%s:%d", libdream.DefaultHost, config.Others["http"])

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
