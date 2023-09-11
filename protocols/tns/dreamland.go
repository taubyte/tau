package tns

import (
	"context"
	"fmt"

	iface "github.com/taubyte/go-interfaces/common"
	commonSpecs "github.com/taubyte/go-specs/common"
	tauConfig "github.com/taubyte/tau/config"
	"github.com/taubyte/tau/libdream"
)

func init() {
	if err := libdream.Registry.Set(commonSpecs.TNS, createTNSService, nil); err != nil {
		panic(err)
	}
}

func createTNSService(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
	return New(ctx, &tauConfig.Node{
		Root:        config.Root,
		P2PListen:   []string{fmt.Sprintf(libdream.DefaultP2PListenFormat, config.Port)},
		P2PAnnounce: []string{fmt.Sprintf(libdream.DefaultP2PListenFormat, config.Port)},
		DevMode:     true,
		SwarmKey:    config.SwarmKey,
		Databases:   config.Databases,
	})
}
