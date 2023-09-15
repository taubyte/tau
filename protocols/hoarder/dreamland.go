package hoarder

import (
	"context"
	"fmt"

	iface "github.com/taubyte/go-interfaces/common"
	commonSpecs "github.com/taubyte/go-specs/common"
	tauConfig "github.com/taubyte/tau/config"
	"github.com/taubyte/tau/libdream"
)

func init() {
	if err := libdream.Registry.Set(commonSpecs.Hoarder, createService, nil); err != nil {
		panic(err)
	}
}

func createService(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
	// TODO: This can be generic
	return New(ctx, &tauConfig.Node{
		Root:        config.Root,
		P2PListen:   []string{fmt.Sprintf(libdream.DefaultP2PListenFormat, config.Port)},
		P2PAnnounce: []string{fmt.Sprintf(libdream.DefaultP2PListenFormat, config.Port)},
		DevMode:     true,
		SwarmKey:    config.SwarmKey,
		Databases:   config.Databases,
	})
}
