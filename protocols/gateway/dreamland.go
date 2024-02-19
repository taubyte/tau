package gateway

import (
	"context"

	iface "github.com/taubyte/go-interfaces/common"
	commonSpecs "github.com/taubyte/go-specs/common"
	"github.com/taubyte/tau/libdream"
	"github.com/taubyte/tau/libdream/common"
)

func init() {
	if err := libdream.Registry.Set(commonSpecs.Gateway, createGateway, nil); err != nil {
		panic(err)
	}
}

func createGateway(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
	return New(ctx, common.NewDreamlandConfig(config))
}
