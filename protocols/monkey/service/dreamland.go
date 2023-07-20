package service

import (
	"context"
	"fmt"

	"github.com/taubyte/dreamland/core/common"
	dreamlandRegistry "github.com/taubyte/dreamland/core/registry"
	iface "github.com/taubyte/go-interfaces/common"
	odoConfig "github.com/taubyte/odo/config"
)

func init() {
	dreamlandRegistry.Registry.Monkey.Service = createService
}

func createService(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
	serviceConfig := &odoConfig.Protocol{
		Root:        config.Root,
		P2PListen:   []string{fmt.Sprintf(common.DefaultP2PListenFormat, config.Port)},
		P2PAnnounce: []string{fmt.Sprintf(common.DefaultP2PListenFormat, config.Port)},
		DevMode:     true,
		DomainValidation: odoConfig.DomainValidation{
			PublicKey: config.PublicKey,
		},
		SwarmKey: config.SwarmKey,
	}

	return New(ctx, serviceConfig)
}
