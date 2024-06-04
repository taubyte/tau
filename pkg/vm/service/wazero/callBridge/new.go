package callbridge

import (
	"github.com/taubyte/tau/core/vm"
	"github.com/tetratelabs/wazero/api"
)

func New(module api.Module) vm.Module {
	return &callContext{wazero: module}
}
