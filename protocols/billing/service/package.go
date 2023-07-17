package service

import (
	"context"

	serviceIface "github.com/taubyte/go-interfaces/services"
	"github.com/taubyte/go-interfaces/services/common"
	commonIface "github.com/taubyte/go-interfaces/services/common"
)

type packageInterface struct{}

func (packageInterface) Config() *common.GenericConfig {
	return &commonIface.GenericConfig{}
}

func (packageInterface) New(ctx context.Context, cnf *common.GenericConfig) (serviceIface.Service, error) {
	return New(ctx, cnf)
}

var _ common.Package = packageInterface{}

func Package() packageInterface {
	return packageInterface{}
}
