package monkey

import (
	iface "github.com/taubyte/go-interfaces/common"
	commonSpecs "github.com/taubyte/go-specs/common"
	"github.com/taubyte/tau/libdream"
	"github.com/taubyte/tau/libdream/common"
)

func init() {
	if err := libdream.Registry.Set(commonSpecs.Monkey, createService, nil); err != nil {
		panic(err)
	}
}

func createService(u *libdream.Universe, config *iface.ServiceConfig) (iface.Service, error) {
	return New(u.Context(), common.NewDreamlandConfig(u, config))
}
