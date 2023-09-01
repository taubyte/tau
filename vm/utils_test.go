package vm

import (
	"errors"

	"github.com/taubyte/go-interfaces/services/substrate/components"
	"github.com/taubyte/go-interfaces/vm"
	structureSpec "github.com/taubyte/go-specs/structure"
	plugins "github.com/taubyte/vm-core-plugins/taubyte"
)

func init() {
	plugins.With = func(pi vm.PluginInstance) (plugins.Instance, error) { return nil, nil }
}

func newMockServiceable() *mockServiceable {
	return &mockServiceable{
		service: newMockService(),
	}
}

type mockServiceable struct {
	components.Serviceable
	service *mockService
}

func newMockService() *mockService {
	return &mockService{vm: newMockVm()}
}

type mockService struct {
	components.ServiceComponent
	vm *mockVm
}

func newMockVm() *mockVm {
	return &mockVm{}
}

type mockVm struct {
	vm.Service
	failInstance bool
}

type mockInstance struct {
	vm.Instance
}

type mockRuntime struct {
	vm.Runtime
}

type mockCache struct {
	components.Cache
}

func (*mockServiceable) Project() string                        { return "" }
func (*mockServiceable) Application() string                    { return "" }
func (*mockServiceable) Id() string                             { return "" }
func (*mockServiceable) Structure() *structureSpec.Function     { return &structureSpec.Function{} }
func (m *mockServiceable) Service() components.ServiceComponent { return m.service }
func (*mockServiceable) Close()                                 {}

func (*mockService) Verbose() bool           { return false }
func (m *mockService) Vm() vm.Service        { return m.vm }
func (*mockService) Orbitals() []vm.Plugin   { return nil }
func (*mockService) Cache() components.Cache { return &mockCache{} }

func (*mockCache) Remove(components.Serviceable) {}

var errorTest = errors.New("test fail")

func (m *mockVm) New(context vm.Context, config vm.Config) (vm.Instance, error) {
	if m.failInstance {
		return nil, errorTest
	}

	return &mockInstance{}, nil
}
func (*mockInstance) Runtime(*vm.HostModuleDefinitions) (vm.Runtime, error) {
	return &mockRuntime{}, nil
}
func (*mockInstance) Close() error { return nil }
func (*mockRuntime) Close() error  { return nil }
func (*mockRuntime) Attach(plugin vm.Plugin) (vm.PluginInstance, vm.ModuleInstance, error) {
	return nil, nil, nil
}
