package vm

import (
	"context"
	"testing"

	wazyapi "github.com/samyfodil/wazy/api"
	"github.com/taubyte/tau/core/vm"
)

// errnoLike mirrors the go-sdk errno.Error result type (named uint32) that the
// ~209 W_ host methods return, so the benchmark exercises the same Convert path.
type errnoLike uint32

// sampleHostMethod is a representative W_-style host method: ctx + module +
// two uint32 params, one named-uint32 result. It does trivial work so the
// measurement is dominated by the call adapter, not the body.
func sampleHostMethod(ctx context.Context, module vm.Module, a, b uint32) errnoLike {
	return errnoLike(a + b)
}

// BenchmarkHostCallReflected measures the per-call cost of the Phase-1
// reflection adapter (reflect.Value.Call each invocation) that still backs the
// hand-written W_ SDK methods.
func BenchmarkHostCallReflected(b *testing.B) {
	fd, err := reflectedHandler(sampleHostMethod)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	var mod wazyapi.Module // unused by the body
	stack := make([]uint64, 2)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stack[0], stack[1] = 20, 22
		fd.fn(ctx, mod, stack)
	}
}

// BenchmarkHostCallTyped measures the reflection-free equivalent Phase 2 would
// produce: a monomorphized stack adapter that decodes/encodes directly.
func BenchmarkHostCallTyped(b *testing.B) {
	stackFn := func(ctx context.Context, module vm.Module, stack []uint64) {
		a := wazyapi.DecodeU32(stack[0])
		bb := wazyapi.DecodeU32(stack[1])
		stack[0] = wazyapi.EncodeU32(uint32(sampleHostMethod(ctx, module, a, bb)))
	}
	ctx := context.Background()
	stack := make([]uint64, 2)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stack[0], stack[1] = 20, 22
		stackFn(ctx, nil, stack)
	}
}
