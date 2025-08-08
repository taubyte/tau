package auth

import (
	iface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/common"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
)

func init() {
	if err := dream.Registry.Set(commonSpecs.Auth, createAuthService, nil); err != nil {
		panic(err)
	}
}

func createAuthService(u *dream.Universe, config *iface.ServiceConfig) (iface.Service, error) {
	return New(u.Context(), common.NewDreamConfig(u, config))
}
