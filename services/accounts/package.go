package accounts

import (
	"context"

	serviceIface "github.com/taubyte/tau/core/services"
	"github.com/taubyte/tau/pkg/config"
)

type protoCommandIface struct{}

func (protoCommandIface) New(ctx context.Context, cnf config.Config) (serviceIface.Service, error) {
	return New(ctx, cnf)
}

// Package returns the accounts service factory used by the node registry
// (cli/node/node.go).
func Package() config.ProtoCommandIface {
	return protoCommandIface{}
}
