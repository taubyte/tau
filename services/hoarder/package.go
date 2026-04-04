package hoarder

import (
	"context"

	serviceIface "github.com/taubyte/tau/core/services"
	"github.com/taubyte/tau/pkg/config"
)

type protoCommandIface struct{}

func (protoCommandIface) New(ctx context.Context, cnf config.Config) (serviceIface.Service, error) {
	return New(ctx, cnf)
}

func Package() config.ProtoCommandIface {
	return protoCommandIface{}
}
