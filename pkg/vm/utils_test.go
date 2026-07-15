package vm

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/taubyte/tau/core/vm"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	fixtures "github.com/taubyte/tau/pkg/vm/fixtures/wasm"
	sources "github.com/taubyte/tau/pkg/vm/sources/taubyte"
	"github.com/taubyte/tau/pkg/vm/test_utils"
)

var (
	theAnswer uint32 = 42

	mockMemoryDef = &vm.HostModuleMemoryDefinition{
		Name: "mock",
		Pages: struct {
			Min   uint64
			Max   uint64
			Maxed bool
		}{
			Min:   0,
			Max:   10,
			Maxed: false,
		},
	}

	mockGlobalDef = &vm.HostModuleGlobalDefinition{
		Name:  "mock",
		Value: "hello_world",
	}

	testFunc = &vm.HostModuleFunctionDefinition{
		Name: "_test",
		Handler: func(ctx context.Context, val uint32) uint32 {
			return val
		},
	}
)

func newService() (vm.Context, vm.Service, error) {
	test_utils.ResetVars()

	ctx, ctxC := context.WithCancel(context.Background())
	defer ctxC()

	_, loader, _, _, _, err := test_utils.Loader(ctx, bytes.NewReader(fixtures.Artifact))
	if err != nil {
		return nil, nil, err
	}

	source := sources.New(loader)
	tctx, err := test_utils.Context()
	if err != nil {
		return nil, nil, err
	}

	return tctx, New(tctx.Context(), source), nil
}

func newInstance() (vm.Instance, error) {
	ctx, service, err := newService()
	if err != nil {
		return nil, err
	}

	return service.New(ctx, vm.Config{})
}

func newBasicRuntime() (vm.Runtime, error) {
	instance, err := newInstance()
	if err != nil {
		return nil, err
	}

	return instance.Runtime(nil)
}

func newRuntimeWithHostDefs() (vm.Runtime, error) {
	instance, err := newInstance()
	if err != nil {
		return nil, err
	}

	return instance.Runtime(
		&vm.HostModuleDefinitions{
			Functions: []*vm.HostModuleFunctionDefinition{testFunc},
			Memories:  []*vm.HostModuleMemoryDefinition{mockMemoryDef},
			Globals:   []*vm.HostModuleGlobalDefinition{mockGlobalDef},
		})
}

func newModuleInstance() (vm.ModuleInstance, error) {
	runtime, err := newRuntimeWithHostDefs()
	if err != nil {
		return nil, err
	}

	return runtime.Module(functionSpec.ModuleName(test_utils.TestFunc.Name))

}

func newFuncs(functionNames []string) (map[string]vm.FunctionInstance, error) {
	mi, err := newModuleInstance()
	if err != nil {
		return nil, err
	}

	functions := make(map[string]vm.FunctionInstance, 0)
	for _, name := range functionNames {
		function, err := mi.Function(name)
		if err != nil {
			return nil, err
		}

		functions[name] = function
	}

	return functions, nil
}

func compareError(retrieved, expected interface{}) error {
	return fmt.Errorf("got `%d` expected `%d`", retrieved, expected)
}

func callFuncs(functionNames []string) error {
	functions, err := newFuncs(functionNames)
	if err != nil {
		return err
	}

	for name, function := range functions {
		ret, err := function.RawCall(context.TODO(), uint64(theAnswer))
		if err != nil {
			return err
		}
		if len(ret) == 0 {
			return fmt.Errorf("function `%s` returned no value", name)
		}

		switch name {
		case "tou32":
			if got := uint32(ret[0]); got != theAnswer {
				return compareError(got, theAnswer)
			}
		case "tof32":
			if got := math.Float32frombits(uint32(ret[0])); got != float32(theAnswer) {
				return compareError(got, theAnswer)
			}
		case "toi32":
			if got := int32(ret[0]); got != int32(theAnswer) {
				return compareError(got, theAnswer)
			}
		case "tof64":
			if got := math.Float64frombits(ret[0]); got != float64(theAnswer) {
				return compareError(got, theAnswer)
			}
		}
	}

	return nil
}

func assertError(t *testing.T, err error) {
	if err == nil {
		t.Error("expected error")
		t.FailNow()
	}
}
