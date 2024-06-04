package substrate

import (
	"context"

	"github.com/taubyte/tau/config"
	serviceIface "github.com/taubyte/tau/core/services"
)

type protoCommandIface struct{}

func (protoCommandIface) New(ctx context.Context, cnf *config.Node) (serviceIface.Service, error) {
	return New(ctx, cnf)
}

func Package() config.ProtoCommandIface {
	return protoCommandIface{}
}
