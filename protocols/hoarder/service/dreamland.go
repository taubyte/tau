package service

import (
	"context"
	"fmt"

	"bitbucket.org/taubyte/dreamland/common"
	dreamlandRegistry "bitbucket.org/taubyte/dreamland/registry"
	iface "github.com/taubyte/go-interfaces/common"
	commonIface "github.com/taubyte/go-interfaces/services/common"
)

func init() {
	dreamlandRegistry.Registry.Hoarder.Service = createService
}

func createService(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
	return New(ctx, &commonIface.GenericConfig{
		Root:        config.Root,
		P2PListen:   []string{fmt.Sprintf(common.DefaultP2PListenFormat, config.Port)},
		P2PAnnounce: []string{fmt.Sprintf(common.DefaultP2PListenFormat, config.Port)},
		Bootstrap:   false,
		DevMode:     true,
		SwarmKey:    config.SwarmKey,
	})
}
