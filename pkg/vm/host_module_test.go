package vm

import (
	"context"
	"reflect"
	"testing"

	wazyapi "github.com/samyfodil/wazy/api"
	"github.com/taubyte/tau/core/vm"
	"gotest.tools/v3/assert"
)

// fakeWazyModule satisfies wazy api.Module for the single method our test
// handlers touch; the embedded nil interface is never invoked.
type fakeWazyModule struct {
	wazyapi.Module
	name string
}

func (f *fakeWazyModule) Name() string { return f.name }

// TestReflectedHandlerSignature asserts the wasm signature is derived from a
// numeric handler and the stack adapter decodes/encodes correctly.
func TestReflectedHandlerSignature(t *testing.T) {
	fd, err := reflectedHandler(func(ctx context.Context, val uint32) uint32 {
		return val + 1
	})
	assert.NilError(t, err)
	assert.DeepEqual(t, fd.params, []vm.ValueType{vm.ValueTypeI32})
	assert.DeepEqual(t, fd.results, []vm.ValueType{vm.ValueTypeI32})

	stack := []uint64{41}
	fd.fn(context.TODO(), nil, stack)
	assert.Equal(t, stack[0], uint64(42))
}

// TestReflectedHandlerSignatureCache asserts two handlers sharing the same Go
// func type reuse the cached signature.
func TestReflectedHandlerSignatureCache(t *testing.T) {
	mk := func(ctx context.Context, val uint32) uint32 { return val }
	tp := reflect.TypeOf(mk)
	reflectedSignatures.Delete(tp)

	_, err := reflectedHandler(func(ctx context.Context, val uint32) uint32 { return val + 1 })
	assert.NilError(t, err)
	first, ok := reflectedSignatures.Load(tp)
	assert.Assert(t, ok)

	_, err = reflectedHandler(func(ctx context.Context, val uint32) uint32 { return val * 2 })
	assert.NilError(t, err)
	second, _ := reflectedSignatures.Load(tp)
	assert.Equal(t, first, second, "same func type must reuse one cached signature")
}

// TestReflectedHandlerModuleParam asserts a handler taking a vm.Module param
// receives a working module (wrapped from the wazy api.Module) at call time.
func TestReflectedHandlerModuleParam(t *testing.T) {
	fd, err := reflectedHandler(func(ctx context.Context, module vm.Module, val uint32) uint32 {
		return uint32(len(module.Name())) + val
	})
	assert.NilError(t, err)
	// module + ctx are not wasm params
	assert.DeepEqual(t, fd.params, []vm.ValueType{vm.ValueTypeI32})

	stack := []uint64{7}
	fd.fn(context.TODO(), &fakeWazyModule{name: "mock"}, stack) // len("mock")=4
	assert.Equal(t, stack[0], uint64(11))
}

// TestStackFastPath asserts the reflection-free path registers a stack adapter
// verbatim with the caller-supplied signature.
func TestStackFastPath(t *testing.T) {
	hm := &hostModule{name: "test", functions: make(map[string]functionDef)}

	err := hm.Functions(&vm.HostModuleFunctionDefinition{
		Name:        "double",
		Stack:       func(ctx context.Context, module vm.Module, stack []uint64) { stack[0] *= 2 },
		ParamTypes:  []vm.ValueType{vm.ValueTypeI32},
		ResultTypes: []vm.ValueType{vm.ValueTypeI32},
	})
	assert.NilError(t, err)

	fd := hm.functions["double"]
	assert.DeepEqual(t, fd.params, []vm.ValueType{vm.ValueTypeI32})
	stack := []uint64{21}
	fd.fn(context.TODO(), nil, stack)
	assert.Equal(t, stack[0], uint64(42))
}
