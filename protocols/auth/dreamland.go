package auth

import (
	"context"

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

func createAuthService(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
	return New(ctx, common.NewDreamlandConfig(config))
}
