package service

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/taubyte/tau/core/vm"
	api "github.com/tetratelabs/wazero/api"
	"gotest.tools/v3/assert"
)

// fakeWazeroModule satisfies api.Module for the single method our test handler
// touches; the embedded nil interface is never invoked.
type fakeWazeroModule struct {
	api.Module
	name string
}

func (f *fakeWazeroModule) Name() string { return f.name }

// TestConvertToHandlerSignatureCache converts two handlers sharing the same Go
// func type and asserts the cached signature (funcType) is reused, and that
// both converted functions remain independently callable.
func TestConvertToHandlerSignatureCache(t *testing.T) {
	hm := &hostModule{
		name:      "test",
		functions: make(map[string]functionDef),
	}

	def1 := &vm.HostModuleFunctionDefinition{
		Name: "same_sig_1",
		Handler: func(ctx context.Context, val uint32) uint32 {
			return val + 1
		},
	}
	def2 := &vm.HostModuleFunctionDefinition{
		Name: "same_sig_2",
		Handler: func(ctx context.Context, val uint32) uint32 {
			return val * 2
		},
	}

	h1, err := hm.convertToHandler(def1)
	assert.NilError(t, err)

	h2, err := hm.convertToHandler(def2)
	assert.NilError(t, err)

	f1 := reflect.ValueOf(h1)
	f2 := reflect.ValueOf(h2)

	// identical handler signatures share one cached func type
	if f1.Type() != f2.Type() {
		t.Fatalf("expected same converted func type on cache hit, got %s vs %s", f1.Type(), f2.Type())
	}

	ret1 := f1.Call([]reflect.Value{reflect.ValueOf(context.TODO()), reflect.ValueOf(uint32(41))})
	if got := ret1[0].Interface().(uint32); got != 42 {
		t.Fatalf("handler 1 returned %d, expected 42", got)
	}

	ret2 := f2.Call([]reflect.Value{reflect.ValueOf(context.TODO()), reflect.ValueOf(uint32(21))})
	if got := ret2[0].Interface().(uint32); got != 42 {
		t.Fatalf("handler 2 returned %d, expected 42", got)
	}
}

// TestConvertToHandlerModuleParam asserts a handler taking a vm.Module param
// gets it swapped to the wazero api.Module type in the converted signature,
// and that at call time it is converted back into a working vm.Module.
func TestConvertToHandlerModuleParam(t *testing.T) {
	hm := &hostModule{
		name:      "test",
		functions: make(map[string]functionDef),
	}

	def := &vm.HostModuleFunctionDefinition{
		Name: "with_module",
		Handler: func(ctx context.Context, module vm.Module, val uint32) string {
			return fmt.Sprintf("%s:%d", module.Name(), val)
		},
	}

	handler, err := hm.convertToHandler(def)
	assert.NilError(t, err)

	f := reflect.ValueOf(handler)

	if f.Type().In(1) != wazeroModuleType {
		t.Fatalf("expected param 1 to be swapped to the wazero api.Module type, got %s", f.Type().In(1))
	}

	fake := &fakeWazeroModule{name: "mock_module"}

	ret := f.Call([]reflect.Value{
		reflect.ValueOf(context.TODO()),
		reflect.ValueOf(fake),
		reflect.ValueOf(uint32(7)),
	})

	if got := ret[0].Interface().(string); got != "mock_module:7" {
		t.Fatalf("handler returned %q, expected %q", got, "mock_module:7")
	}
}
