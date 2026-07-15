package vm

import (
	"testing"

	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm/mocks"
	"gotest.tools/v3/assert"
)

func TestService(t *testing.T) {
	_, service, err := newService()
	assert.NilError(t, err)

	err = service.Close()
	assert.NilError(t, err)
}

func TestModuleFunctionFailure(t *testing.T) {
	module, err := newModuleInstance()
	assert.NilError(t, err)

	_, err = module.Function("hello_world")
	assertError(t, err)
}

func TestInstance(t *testing.T) {
	instance, err := newInstance()
	assert.NilError(t, err)

	if instance.Stderr() == nil {
		t.Error("stderr is nil")
	}

	if instance.Filesystem() == nil {
		t.Error("stderr is nil")
	}

	if instance.Stdout() == nil {
		t.Error("stdout is nil")
	}

	if instance.Context() == nil {
		t.Error("context is nil")
	}

	err = instance.Close()
	assert.NilError(t, err)
}

func TestRuntime(t *testing.T) {
	instance, err := newInstance()
	assert.NilError(t, err)

	_, err = instance.Runtime(nil)
	assert.NilError(t, err)

	if instance.Stderr() == nil {
		t.Error("stderr is nil")
	}

	if instance.Stdout() == nil {
		t.Error("stdout is nil")
	}

	err = instance.Close()
	assert.NilError(t, err)

	// duplicate function error
	_, err = instance.Runtime(
		&vm.HostModuleDefinitions{
			Functions: []*vm.HostModuleFunctionDefinition{testFunc, testFunc},
		})
	assertError(t, err)

	// duplicate global error
	_, err = instance.Runtime(
		&vm.HostModuleDefinitions{
			Functions: []*vm.HostModuleFunctionDefinition{testFunc},
			Globals:   []*vm.HostModuleGlobalDefinition{mockGlobalDef, mockGlobalDef},
		})
	assertError(t, err)

	// duplicate memory error
	_, err = instance.Runtime(
		&vm.HostModuleDefinitions{
			Functions: []*vm.HostModuleFunctionDefinition{testFunc},
			Memories:  []*vm.HostModuleMemoryDefinition{mockMemoryDef, mockMemoryDef},
		})
	assertError(t, err)

}

func TestRuntimeCall(t *testing.T) {
	err := callFuncs([]string{"tou32", "tof32", "toi32", "tof64"})
	assert.NilError(t, err)
}

func TestRuntimePlugin(t *testing.T) {
	runtime, err := newBasicRuntime()
	assert.NilError(t, err)

	plugin := mocks.NewPlugin(false)
	_, _, err = runtime.Attach(plugin)
	assert.NilError(t, err)

	// nil plugin error
	_, _, err = runtime.Attach(nil)
	assertError(t, err)

	// mock New error
	plugin = mocks.NewPlugin(true)
	_, _, err = runtime.Attach(plugin)
	assertError(t, err)
}
