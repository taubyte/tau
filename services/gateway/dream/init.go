package dream

import (
	iface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/common"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/services/gateway"
)

func init() {
	if err := dream.Registry.Set(commonSpecs.Gateway, createGateway, nil); err != nil {
		panic(err)
	}
}

func createGateway(u *dream.Universe, config *iface.ServiceConfig) (iface.Service, error) {
	cfg, err := common.NewConfig(u, config)
	if err != nil {
		return nil, err
	}
	return gateway.New(u.Context(), cfg)
}
