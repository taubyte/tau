package vm

import (
	"fmt"

	"github.com/taubyte/tau/core/vm"
)

var _ vm.ModuleInstance = &moduleInstance{}

func (m *moduleInstance) Function(name string) (vm.FunctionInstance, error) {
	funcInst := m.module.ExportedFunction(name)
	if funcInst == nil {
		return nil, fmt.Errorf("Function (%s).`%s` does not exist", m.module.Name(), name)
	}

	f := &funcInstance{
		module:   m,
		function: funcInst,
	}

	return f, nil
}

func (m *moduleInstance) Memory() vm.Memory {
	return m.module.Memory()
}
