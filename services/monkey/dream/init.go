package monkey

import (
	iface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/common"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/services/monkey"
)

func init() {
	if err := dream.Registry.Set(commonSpecs.Monkey, createService, nil); err != nil {
		panic(err)
	}
}

func createService(u *dream.Universe, config *iface.ServiceConfig) (iface.Service, error) {
	return monkey.New(u.Context(), common.NewDreamConfig(u, config))
}
