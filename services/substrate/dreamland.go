package substrate

import (
	iface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/common"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/services/substrate/mocks/counters"
)

func init() {
	if err := dream.Registry.Set(commonSpecs.Substrate, createNodeService, nil); err != nil {
		panic(err)
	}
}

func createNodeService(u *dream.Universe, config *iface.ServiceConfig) (iface.Service, error) {
	service, err := New(u.Context(), common.NewDreamlandConfig(u, config))
	if err != nil {
		return nil, err
	}

	service.components.counters = counters.New(service)

	return service, nil
}
