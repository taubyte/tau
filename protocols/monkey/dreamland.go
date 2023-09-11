package monkey

import (
	"context"
	"fmt"

	iface "github.com/taubyte/go-interfaces/common"
	commonSpecs "github.com/taubyte/go-specs/common"
	tauConfig "github.com/taubyte/tau/config"
	"github.com/taubyte/tau/libdream"
)

func init() {
	if err := libdream.Registry.Set(commonSpecs.Monkey, createService, nil); err != nil {
		panic(err)
	}
}

func createService(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
	serviceConfig := &tauConfig.Node{
		Root:        config.Root,
		P2PListen:   []string{fmt.Sprintf(libdream.DefaultP2PListenFormat, config.Port)},
		P2PAnnounce: []string{fmt.Sprintf(libdream.DefaultP2PListenFormat, config.Port)},
		DevMode:     true,
		DomainValidation: tauConfig.DomainValidation{
			PublicKey: config.PublicKey,
		},
		SwarmKey:  config.SwarmKey,
		Databases: config.Databases,
	}

	return New(ctx, serviceConfig)
}
