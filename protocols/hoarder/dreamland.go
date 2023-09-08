package hoarder

import (
	"context"
	"fmt"

	iface "github.com/taubyte/go-interfaces/common"
	tauConfig "github.com/taubyte/tau/config"
	"github.com/taubyte/tau/libdream"
	"github.com/taubyte/tau/libdream/common"
)

func init() {
	libdream.Registry.Hoarder.Service = createService
}

func createService(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
	return New(ctx, &tauConfig.Node{
		Root:        config.Root,
		P2PListen:   []string{fmt.Sprintf(common.DefaultP2PListenFormat, config.Port)},
		P2PAnnounce: []string{fmt.Sprintf(common.DefaultP2PListenFormat, config.Port)},
		DevMode:     true,
		SwarmKey:    config.SwarmKey,
		Databases:   config.Databases,
	})
}
