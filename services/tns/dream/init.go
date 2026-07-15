package dream

import (
	iface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/common"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
	tns "github.com/taubyte/tau/services/tns"
)

func init() {
	if err := dream.Registry.Set(commonSpecs.TNS, createTNSService, nil); err != nil {
		panic(err)
	}
}

func createTNSService(u *dream.Universe, config *iface.ServiceConfig) (iface.Service, error) {
	cfg, err := common.NewConfig(u, config)
	if err != nil {
		return nil, err
	}
	svc, err := tns.New(u.Context(), cfg)
	if err != nil {
		return nil, err
	}
	if err := common.StartBeacon(u.Context(), cfg, svc.Node(), commonSpecs.TNS); err != nil {
		return nil, err
	}
	return svc, nil
}
