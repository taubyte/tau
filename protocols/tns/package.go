package tns

import (
	"context"

	serviceIface "github.com/taubyte/go-interfaces/services"
	"github.com/taubyte/tau/config"
)

type packageInterface struct{}

func (packageInterface) New(ctx context.Context, cnf *config.Protocol) (serviceIface.Service, error) {
	return New(ctx, cnf)
}

func Package() config.Package {
	return packageInterface{}
}
