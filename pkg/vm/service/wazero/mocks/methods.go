package mocks

import (
	"context"
	"errors"

	"github.com/taubyte/tau/core/vm"
)

func (m *mockPlugin) New(instance vm.Instance) (vm.PluginInstance, error) {
	if instance == nil {
		return nil, errors.New("instance is nil")
	}

	if m.InstanceFail {
		return nil, errors.New("mock failure")
	}

	return &mockPluginInstance{}, nil
}

func (m *mockPlugin) Close() error {
	return nil
}

func (m *mockPlugin) Name() string {
	return "mock"
}

func (m *mockPluginInstance) Load(hostModule vm.HostModule) (vm.ModuleInstance, error) {
	if hostModule == nil {
		return nil, errors.New("host module is nil")
	}

	return &mockModuleInstance{}, nil
}

func (m *mockPluginInstance) Close() error {
	return nil
}

func (m *mockPluginInstance) LoadFactory(factory vm.Factory, hm vm.HostModule) error {
	if factory == nil || hm == nil {
		return errors.New("params are nil")
	}

	return nil
}

func (m *mockModuleInstance) Function(name string) (vm.FunctionInstance, error) {
	if len(name) == 0 {
		return nil, errors.New("name is empty")
	}

	return &mockFunctionInstance{}, nil
}

func (m *mockFunctionInstance) Cancel() error {
	return nil
}

func (m *mockFunctionInstance) Call(context.Context, ...interface{}) vm.Return {
	return &mockReturn{}
}

func (m *mockReturn) Error() error {
	return nil
}

func (m *mockReturn) Reflect(...interface{}) error {
	return nil
}
