package service

import (
	"context"

	serviceIface "github.com/taubyte/tau/core/services"
	"github.com/taubyte/tau/pkg/config"
)

// Package interface implementation
type protoCommandIface struct{}

func (protoCommandIface) New(ctx context.Context, cnf config.Config) (serviceIface.Service, error) {
	return New(ctx, cnf)
}

func Package() config.ProtoCommandIface {
	return protoCommandIface{}
}
