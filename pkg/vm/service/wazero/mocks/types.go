package mocks

import (
	"github.com/taubyte/tau/core/vm"
)

type MockedPlugin interface {
	vm.Plugin
}

type mockPlugin struct {
	InstanceFail bool
}

type MockedPluginInstance interface {
	vm.PluginInstance
}

type mockPluginInstance struct{}

type MockedModuleInstance interface {
	vm.ModuleInstance
}

type mockModuleInstance struct {
	vm.ModuleInstance
}

type MockedModule interface {
	vm.Module
}

type MockedFunctionInstance interface {
	vm.FunctionInstance
}

type mockFunctionInstance struct {
	vm.FunctionInstance
}

type MockedReturn interface {
	vm.Return
}

type mockReturn struct{ vm.Return }
