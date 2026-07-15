package vm

import (
	"context"
	"testing"
)

type errnoLike uint32

func TestHostFn2NamedResult(t *testing.T) {
	def := HostFn2("add", func(ctx context.Context, m Module, a, b uint32) errnoLike {
		return errnoLike(a + b)
	})
	if def.Name != "add" {
		t.Fatalf("name = %q", def.Name)
	}
	// two i32 params, one i32 result (errnoLike is a named uint32)
	assertTypes(t, "params", def.ParamTypes, []ValueType{ValueTypeI32, ValueTypeI32})
	assertTypes(t, "results", def.ResultTypes, []ValueType{ValueTypeI32})

	s := []uint64{20, 22}
	def.Stack(context.Background(), nil, s)
	if s[0] != 42 {
		t.Fatalf("result = %d, want 42", s[0])
	}
}

func TestHostFnI64Param(t *testing.T) {
	var got int64
	def := HostProc1("sleep", func(ctx context.Context, m Module, dur int64) { got = dur })
	assertTypes(t, "params", def.ParamTypes, []ValueType{ValueTypeI64})

	def.Stack(context.Background(), nil, []uint64{uint64(1 << 40)})
	if got != 1<<40 {
		t.Fatalf("i64 param = %d, want %d", got, int64(1<<40))
	}
}

func TestHostProc0Void(t *testing.T) {
	called := false
	def := HostProc0("ready", func(ctx context.Context, m Module) { called = true })
	if len(def.ParamTypes) != 0 || len(def.ResultTypes) != 0 {
		t.Fatal("void func must have no param/result types")
	}
	def.Stack(context.Background(), nil, nil)
	if !called {
		t.Fatal("not called")
	}
}

func assertTypes(t *testing.T, what string, got, want []ValueType) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len = %d, want %d", what, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s[%d] = %#x, want %#x", what, i, got[i], want[i])
		}
	}
}
