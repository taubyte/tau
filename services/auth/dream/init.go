package auth

import (
	iface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/common"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/services/auth"
)

func init() {
	if err := dream.Registry.Set(commonSpecs.Auth, createAuthService, nil); err != nil {
		panic(err)
	}
}

func createAuthService(u *dream.Universe, config *iface.ServiceConfig) (iface.Service, error) {
	cfg, err := common.NewConfig(u, config)
	if err != nil {
		return nil, err
	}
	svc, err := auth.New(u.Context(), cfg)
	if err != nil {
		return nil, err
	}
	if err := common.StartBeacon(u.Context(), cfg, svc.Node(), commonSpecs.Auth); err != nil {
		return nil, err
	}
	return svc, nil
}
