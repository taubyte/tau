package callbridge

import (
	"github.com/taubyte/tau/core/vm"
	"github.com/tetratelabs/wazero/api"
)

var _ vm.Module = &callContext{}

type callContext struct {
	wazero api.Module
}

type memory struct {
	wazero api.Memory
}

type importedFn struct {
	wazero api.Function
}

type importedFnDef struct {
	wazero api.FunctionDefinition
}

type global struct {
	wazero api.Global
}
