package auth

import (
	iface "github.com/taubyte/go-interfaces/common"
	commonSpecs "github.com/taubyte/go-specs/common"
	"github.com/taubyte/tau/libdream"
	"github.com/taubyte/tau/libdream/common"
)

func init() {
	if err := libdream.Registry.Set(commonSpecs.Auth, createAuthService, nil); err != nil {
		panic(err)
	}
}

func createAuthService(u *libdream.Universe, config *iface.ServiceConfig) (iface.Service, error) {
	return New(u.Context(), common.NewDreamlandConfig(u, config))
}
