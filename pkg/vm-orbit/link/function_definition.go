package link

import "github.com/taubyte/tau/core/vm"

func (f *functionDefinition) Name() string {
	return f.name
}

func (f *functionDefinition) ParamTypes() []vm.ValueType {
	return f.args
}

func (f *functionDefinition) ResultTypes() []vm.ValueType {
	return f.rets
}
