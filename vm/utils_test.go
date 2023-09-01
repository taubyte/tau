package vm

import (
	"github.com/taubyte/go-interfaces/services/substrate/components"
	"github.com/taubyte/go-interfaces/vm"
	structureSpec "github.com/taubyte/go-specs/structure"
	plugins "github.com/taubyte/vm-core-plugins/taubyte"
)

func init() {
	plugins.With = func(pi vm.PluginInstance) (plugins.Instance, error) { return nil, nil }
}

type mockServiceable struct {
	components.Serviceable
}

type mockService struct {
	components.ServiceComponent
}

type mockVm struct {
	vm.Service
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

func (*mockServiceable) Project() string                      { return "" }
func (*mockServiceable) Application() string                  { return "" }
func (*mockServiceable) Id() string                           { return "" }
func (*mockServiceable) Structure() *structureSpec.Function   { return &structureSpec.Function{} }
func (*mockServiceable) Service() components.ServiceComponent { return &mockService{} }
func (*mockServiceable) Close()                               {}

func (*mockService) Verbose() bool           { return false }
func (*mockService) Vm() vm.Service          { return &mockVm{} }
func (*mockService) Orbitals() []vm.Plugin   { return nil }
func (*mockService) Cache() components.Cache { return &mockCache{} }

func (*mockCache) Remove(components.Serviceable) {}

func (*mockVm) New(context vm.Context, config vm.Config) (vm.Instance, error) {
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
