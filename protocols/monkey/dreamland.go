package monkey

import (
	"context"
	"fmt"

	iface "github.com/taubyte/go-interfaces/common"
	tauConfig "github.com/taubyte/tau/config"
	"github.com/taubyte/tau/libdream/common"
	dreamlandRegistry "github.com/taubyte/tau/libdream/registry"
)

func init() {
	dreamlandRegistry.Registry.Monkey.Service = createService
}

func createService(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
	serviceConfig := &tauConfig.Node{
		Root:        config.Root,
		P2PListen:   []string{fmt.Sprintf(common.DefaultP2PListenFormat, config.Port)},
		P2PAnnounce: []string{fmt.Sprintf(common.DefaultP2PListenFormat, config.Port)},
		DevMode:     true,
		DomainValidation: tauConfig.DomainValidation{
			PublicKey: config.PublicKey,
		},
		SwarmKey:  config.SwarmKey,
		Databases: config.Databases,
	}

	return New(ctx, serviceConfig)
}
