package starlark

import (
	"testing"

	"go.starlark.net/starlark"
)

var (
	benchmarkValue       starlark.Value
	benchmarkNativeValue any
	benchmarkVM          *vm
)

func BenchmarkContextCall(b *testing.B) {
	vm, err := New(testFiles)
	if err != nil {
		b.Fatalf("failed to create VM: %v", err)
	}

	ctx, err := vm.File("testdata/fibonacci.star")
	if err != nil {
		b.Fatalf("failed to load fibonacci.star: %v", err)
	}

	arg := starlark.MakeInt(10)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		result, err := ctx.Call("fibonacci", arg)
		if err != nil {
			b.Fatalf("call failed: %v", err)
		}
		benchmarkValue = result
	}
}

func BenchmarkContextCallWithNative(b *testing.B) {
	vm, err := New(testFiles)
	if err != nil {
		b.Fatalf("failed to create VM: %v", err)
	}

	if err = vm.Module(new(testModule)); err != nil {
		b.Fatalf("failed to register module: %v", err)
	}

	ctx, err := vm.File("testdata/go.star")
	if err != nil {
		b.Fatalf("failed to load go.star: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		result, err := ctx.CallWithNative("Add2", 5, 3)
		if err != nil {
			b.Fatalf("call with native failed: %v", err)
		}
		benchmarkNativeValue = result
	}
}

func BenchmarkModuleRegistration(b *testing.B) {
	mod := new(testModule)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		vm := &vm{
			builtins: make(map[string]starlark.StringDict),
		}
		if err := vm.Module(mod); err != nil {
			b.Fatalf("module registration failed: %v", err)
		}
		benchmarkVM = vm
	}
}
