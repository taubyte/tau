package service

import (
	"context"

	serviceIface "github.com/taubyte/go-interfaces/services"
	commonIface "github.com/taubyte/go-interfaces/services/common"
)

type packageInterface struct{}

func (packageInterface) Config() *commonIface.GenericConfig {
	return &commonIface.GenericConfig{}
}

func (packageInterface) New(ctx context.Context, cnf *commonIface.GenericConfig) (serviceIface.Service, error) {
	return New(ctx, cnf)
}

var _ commonIface.Package = packageInterface{}

func Package() packageInterface {
	return packageInterface{}
}
