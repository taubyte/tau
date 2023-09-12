package gateway

import (
	"context"
	"fmt"

	iface "github.com/taubyte/go-interfaces/common"
	commonSpecs "github.com/taubyte/go-specs/common"
	tauConfig "github.com/taubyte/tau/config"
	"github.com/taubyte/tau/libdream"
)

func init() {
	if err := libdream.Registry.Set(commonSpecs.Gateway, createGateway, nil); err != nil {
		panic(err)
	}
}

func createGateway(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
	serviceConfig := &tauConfig.Node{
		Ports:       make(map[string]int),
		Root:        config.Root,
		P2PListen:   []string{fmt.Sprintf(libdream.DefaultP2PListenFormat, config.Port)},
		P2PAnnounce: []string{fmt.Sprintf(libdream.DefaultP2PListenFormat, config.Port)},
		DevMode:     true,
		SwarmKey:    config.SwarmKey,
		HttpListen:  fmt.Sprintf("%s:%d", libdream.DefaultHost, config.Others["http"]),
	}

	if result, ok := config.Others["secure"]; ok {
		serviceConfig.EnableHTTPS = result != 0
	}

	return New(ctx, serviceConfig)
}
