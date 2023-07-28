package tns

import (
	"context"

	serviceIface "github.com/taubyte/go-interfaces/services"
	"github.com/taubyte/tau/config"
)

type protoCommandIface struct{}

func (protoCommandIface) New(ctx context.Context, cnf *config.Protocol) (serviceIface.Service, error) {
	return New(ctx, cnf)
}

func Package() config.ProtoCommandIface {
	return protoCommandIface{}
}
