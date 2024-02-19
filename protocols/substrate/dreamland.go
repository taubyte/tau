package substrate

import (
	"context"

	iface "github.com/taubyte/go-interfaces/common"
	commonSpecs "github.com/taubyte/go-specs/common"
	"github.com/taubyte/tau/libdream"
	"github.com/taubyte/tau/libdream/common"
	"github.com/taubyte/tau/protocols/substrate/mocks/counters"
)

func init() {
	if err := libdream.Registry.Set(commonSpecs.Substrate, createNodeService, nil); err != nil {
		panic(err)
	}
}

func createNodeService(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
	service, err := New(ctx, common.NewDreamlandConfig(config))
	if err != nil {
		return nil, err
	}

	service.components.counters = counters.New(service)

	return service, nil
}
