package runtime

import (
	"errors"
	"time"

	"github.com/taubyte/tau/core/services/substrate/components"
	"github.com/taubyte/tau/core/vm"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	plugins "github.com/taubyte/tau/pkg/vm-low-orbit"
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
	runtimeDelay time.Duration
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
func (*mockServiceable) Config() *structureSpec.Function        { return &structureSpec.Function{} }
func (m *mockServiceable) Service() components.ServiceComponent { return m.service }
func (*mockServiceable) Close()                                 {}

func (*mockServiceable) Commit() string { return "2224d3f23b7689c81f0aec961e17ca5ffc85df7e" }
func (*mockServiceable) Branch() string { return "main" }

func (*mockServiceable) AssetId() string {
	return "baguqeerasords4njcts6vs7qvdjfcvgnume4hqohf65zsfguprqphs3icwea"
}

func (*mockService) Verbose() bool           { return false }
func (m *mockService) Vm() vm.Service        { return m.vm }
func (*mockService) Orbitals() []vm.Plugin   { return nil }
func (*mockService) Cache() components.Cache { return &mockCache{} }

func (*mockCache) Remove(components.Serviceable) {}

var errorTest = errors.New("test fail")

func (m *mockVm) New(context vm.Context, config vm.Config) (vm.Instance, error) {
	if m.runtimeDelay > 0 {
		<-time.After(m.runtimeDelay)
	}
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
