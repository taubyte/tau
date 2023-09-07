package gateway

import (
	"context"
	"fmt"

	iface "github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/p2p/peer"
	tauConfig "github.com/taubyte/tau/config"
	dreamlandCommon "github.com/taubyte/tau/libdream/common"
	dreamlandRegistry "github.com/taubyte/tau/libdream/registry"
)

func init() {
	dreamlandRegistry.Registry.Gateway.Service = createGateway

	// TODO: need to actually create a gateway client, currently returning peer node so there isnt error with dreamland
	dreamlandRegistry.Registry.Gateway.Client = func(n peer.Node, cc *iface.ClientConfig) (iface.Client, error) {
		return n, nil
	}
}

func createGateway(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
	serviceConfig := &tauConfig.Node{
		Ports:       make(map[string]int),
		Root:        config.Root,
		P2PListen:   []string{fmt.Sprintf(dreamlandCommon.DefaultP2PListenFormat, config.Port)},
		P2PAnnounce: []string{fmt.Sprintf(dreamlandCommon.DefaultP2PListenFormat, config.Port)},
		DevMode:     true,
		SwarmKey:    config.SwarmKey,
		HttpListen:  fmt.Sprintf("%s:%d", dreamlandCommon.DefaultHost, config.Others["http"]),
	}

	if result, ok := config.Others["secure"]; ok {
		serviceConfig.EnableHTTPS = result != 0
	}

	return New(ctx, serviceConfig)
}
