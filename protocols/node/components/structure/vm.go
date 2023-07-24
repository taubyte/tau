package structure

import (
	"context"

	"github.com/taubyte/go-interfaces/vm"
)

type TestVm struct {
	vm.Service
}

type testInstance struct {
	vm.Instance
}

type testPluginInstance struct {
	vm.PluginInstance
}

type testModule struct {
	vm.ModuleInstance
}

type testFunctionStruct struct {
	vm.FunctionInstance
}

type testReturn struct {
	vm.Return
}

type testRuntime struct {
	vm.Runtime
}

func (*testReturn) Error() error {
	return nil
}

func (*testFunctionStruct) Call(ctx context.Context, args ...interface{}) vm.Return {
	return &testReturn{}
}

func (*testModule) Function(name string) (vm.FunctionInstance, error) {
	return &testFunctionStruct{}, nil
}

func (*TestVm) New(context vm.Context, config vm.Config) (vm.Instance, error) {
	return &testInstance{}, nil
}

func (*testInstance) Close() error {
	return nil
}

func (*testInstance) Call(vm.Runtime, interface{}) error {
	return nil
}

func (*testInstance) Runtime(*vm.HostModuleDefinitions) (vm.Runtime, error) {
	return &testRuntime{}, nil
}

func (*testRuntime) Close() error {
	return nil
}

func (*testRuntime) Module(name string) (vm.ModuleInstance, error) {
	v, ok := AttachedTestFunctions[name]
	if !ok {
		AttachedTestFunctions[name] = 1
	} else {
		AttachedTestFunctions[name] = v + 1
	}
	return &testModule{}, nil
}

func (*testRuntime) Attach(plugin vm.Plugin) (vm.PluginInstance, vm.ModuleInstance, error) {
	return &testPluginInstance{}, nil, nil
}
