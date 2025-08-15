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
	return tns.New(u.Context(), common.NewDreamConfig(u, config))
}
