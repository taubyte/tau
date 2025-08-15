package dream

import (
	iface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/common"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/services/hoarder"
)

func init() {
	if err := dream.Registry.Set(commonSpecs.Hoarder, createService, nil); err != nil {
		panic(err)
	}
}

func createService(u *dream.Universe, config *iface.ServiceConfig) (iface.Service, error) {
	return hoarder.New(u.Context(), common.NewDreamConfig(u, config))
}
