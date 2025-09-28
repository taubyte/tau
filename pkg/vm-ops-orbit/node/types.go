package node

import (
	"context"

	"github.com/taubyte/tau/core/services/substrate"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

type Factory struct {
	helpers.Methods
	parent vm.Instance
	ctx    context.Context

	node substrate.Service
}

var _ vm.Factory = &Factory{}
