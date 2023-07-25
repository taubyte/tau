package monkey

import (
	"context"

	serviceIface "github.com/taubyte/go-interfaces/services"
	"github.com/taubyte/odo/config"
)

type packageInterface struct{}

func (packageInterface) New(ctx context.Context, cnf *config.Protocol) (serviceIface.Service, error) {
	return New(ctx, cnf)
}

func Package() config.Package {
	return packageInterface{}
}
