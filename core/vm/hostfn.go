package vm

import (
	"context"
	"unsafe"
)

// WasmInt is the set of Go types a reflection-free host function may take as a
// parameter or return. The Taubyte host ABI (the W_ methods) is integer-only:
// every parameter is a uint32 pointer/handle and every result is an
// errno.Error (a named uint32) or void. The ~ (approximate) constraint lets
// named types such as errno.Error satisfy it, so HostFnN infers the signature
// straight from a W_ method value with no wrapper.
//
// ponytail: integer-only; add a WasmFloat family + bit-cast decode/encode if a
// host function ever needs f32/f64. None do today.
type WasmInt interface {
	~int32 | ~uint32 | ~int64 | ~uint64
}

// wasmValueType maps a WasmInt to its wasm value type by width (4 bytes -> i32,
// 8 -> i64). Called once per registration (cold path), not per call.
func wasmValueType[T WasmInt]() ValueType {
	var z T
	if unsafe.Sizeof(z) == 4 {
		return ValueTypeI32
	}
	return ValueTypeI64
}

// The HostFnN / HostProcN helpers build a reflection-free HostModuleFunctionDefinition
// from a typed host method: the wasm signature is derived from the Go types at
// compile time and the Stack adapter is monomorphized (no reflect, no per-call
// alloc). Decoding a stack slot to a WasmInt is a plain numeric conversion
// (P(raw)); encoding a result is uint64(r). Both are correct for i32 (low 32
// bits) and i64. Reach for a raw Stack def only for arity the family doesn't
// cover (> 8 params).

func HostProc0(name string, fn func(context.Context, Module)) *HostModuleFunctionDefinition {
	return &HostModuleFunctionDefinition{Name: name, Stack: func(ctx context.Context, m Module, s []uint64) { fn(ctx, m) }}
}

func HostProc1[P1 WasmInt](name string, fn func(context.Context, Module, P1)) *HostModuleFunctionDefinition {
	return &HostModuleFunctionDefinition{
		Name:       name,
		ParamTypes: []ValueType{wasmValueType[P1]()},
		Stack:      func(ctx context.Context, m Module, s []uint64) { fn(ctx, m, P1(s[0])) },
	}
}

func HostProc2[P1, P2 WasmInt](name string, fn func(context.Context, Module, P1, P2)) *HostModuleFunctionDefinition {
	return &HostModuleFunctionDefinition{
		Name:       name,
		ParamTypes: []ValueType{wasmValueType[P1](), wasmValueType[P2]()},
		Stack:      func(ctx context.Context, m Module, s []uint64) { fn(ctx, m, P1(s[0]), P2(s[1])) },
	}
}

func HostProc3[P1, P2, P3 WasmInt](name string, fn func(context.Context, Module, P1, P2, P3)) *HostModuleFunctionDefinition {
	return &HostModuleFunctionDefinition{
		Name:       name,
		ParamTypes: []ValueType{wasmValueType[P1](), wasmValueType[P2](), wasmValueType[P3]()},
		Stack:      func(ctx context.Context, m Module, s []uint64) { fn(ctx, m, P1(s[0]), P2(s[1]), P3(s[2])) },
	}
}

func HostProc4[P1, P2, P3, P4 WasmInt](name string, fn func(context.Context, Module, P1, P2, P3, P4)) *HostModuleFunctionDefinition {
	return &HostModuleFunctionDefinition{
		Name:       name,
		ParamTypes: []ValueType{wasmValueType[P1](), wasmValueType[P2](), wasmValueType[P3](), wasmValueType[P4]()},
		Stack:      func(ctx context.Context, m Module, s []uint64) { fn(ctx, m, P1(s[0]), P2(s[1]), P3(s[2]), P4(s[3])) },
	}
}

func HostProc5[P1, P2, P3, P4, P5 WasmInt](name string, fn func(context.Context, Module, P1, P2, P3, P4, P5)) *HostModuleFunctionDefinition {
	return &HostModuleFunctionDefinition{
		Name:       name,
		ParamTypes: []ValueType{wasmValueType[P1](), wasmValueType[P2](), wasmValueType[P3](), wasmValueType[P4](), wasmValueType[P5]()},
		Stack: func(ctx context.Context, m Module, s []uint64) {
			fn(ctx, m, P1(s[0]), P2(s[1]), P3(s[2]), P4(s[3]), P5(s[4]))
		},
	}
}

func HostProc6[P1, P2, P3, P4, P5, P6 WasmInt](name string, fn func(context.Context, Module, P1, P2, P3, P4, P5, P6)) *HostModuleFunctionDefinition {
	return &HostModuleFunctionDefinition{
		Name:       name,
		ParamTypes: []ValueType{wasmValueType[P1](), wasmValueType[P2](), wasmValueType[P3](), wasmValueType[P4](), wasmValueType[P5](), wasmValueType[P6]()},
		Stack: func(ctx context.Context, m Module, s []uint64) {
			fn(ctx, m, P1(s[0]), P2(s[1]), P3(s[2]), P4(s[3]), P5(s[4]), P6(s[5]))
		},
	}
}

func HostProc7[P1, P2, P3, P4, P5, P6, P7 WasmInt](name string, fn func(context.Context, Module, P1, P2, P3, P4, P5, P6, P7)) *HostModuleFunctionDefinition {
	return &HostModuleFunctionDefinition{
		Name:       name,
		ParamTypes: []ValueType{wasmValueType[P1](), wasmValueType[P2](), wasmValueType[P3](), wasmValueType[P4](), wasmValueType[P5](), wasmValueType[P6](), wasmValueType[P7]()},
		Stack: func(ctx context.Context, m Module, s []uint64) {
			fn(ctx, m, P1(s[0]), P2(s[1]), P3(s[2]), P4(s[3]), P5(s[4]), P6(s[5]), P7(s[6]))
		},
	}
}

func HostProc8[P1, P2, P3, P4, P5, P6, P7, P8 WasmInt](name string, fn func(context.Context, Module, P1, P2, P3, P4, P5, P6, P7, P8)) *HostModuleFunctionDefinition {
	return &HostModuleFunctionDefinition{
		Name:       name,
		ParamTypes: []ValueType{wasmValueType[P1](), wasmValueType[P2](), wasmValueType[P3](), wasmValueType[P4](), wasmValueType[P5](), wasmValueType[P6](), wasmValueType[P7](), wasmValueType[P8]()},
		Stack: func(ctx context.Context, m Module, s []uint64) {
			fn(ctx, m, P1(s[0]), P2(s[1]), P3(s[2]), P4(s[3]), P5(s[4]), P6(s[5]), P7(s[6]), P8(s[7]))
		},
	}
}

func HostFn1[P1, R WasmInt](name string, fn func(context.Context, Module, P1) R) *HostModuleFunctionDefinition {
	return &HostModuleFunctionDefinition{
		Name:        name,
		ParamTypes:  []ValueType{wasmValueType[P1]()},
		ResultTypes: []ValueType{wasmValueType[R]()},
		Stack:       func(ctx context.Context, m Module, s []uint64) { s[0] = uint64(fn(ctx, m, P1(s[0]))) },
	}
}

func HostFn2[P1, P2, R WasmInt](name string, fn func(context.Context, Module, P1, P2) R) *HostModuleFunctionDefinition {
	return &HostModuleFunctionDefinition{
		Name:        name,
		ParamTypes:  []ValueType{wasmValueType[P1](), wasmValueType[P2]()},
		ResultTypes: []ValueType{wasmValueType[R]()},
		Stack:       func(ctx context.Context, m Module, s []uint64) { s[0] = uint64(fn(ctx, m, P1(s[0]), P2(s[1]))) },
	}
}

func HostFn3[P1, P2, P3, R WasmInt](name string, fn func(context.Context, Module, P1, P2, P3) R) *HostModuleFunctionDefinition {
	return &HostModuleFunctionDefinition{
		Name:        name,
		ParamTypes:  []ValueType{wasmValueType[P1](), wasmValueType[P2](), wasmValueType[P3]()},
		ResultTypes: []ValueType{wasmValueType[R]()},
		Stack: func(ctx context.Context, m Module, s []uint64) {
			s[0] = uint64(fn(ctx, m, P1(s[0]), P2(s[1]), P3(s[2])))
		},
	}
}

func HostFn4[P1, P2, P3, P4, R WasmInt](name string, fn func(context.Context, Module, P1, P2, P3, P4) R) *HostModuleFunctionDefinition {
	return &HostModuleFunctionDefinition{
		Name:        name,
		ParamTypes:  []ValueType{wasmValueType[P1](), wasmValueType[P2](), wasmValueType[P3](), wasmValueType[P4]()},
		ResultTypes: []ValueType{wasmValueType[R]()},
		Stack: func(ctx context.Context, m Module, s []uint64) {
			s[0] = uint64(fn(ctx, m, P1(s[0]), P2(s[1]), P3(s[2]), P4(s[3])))
		},
	}
}

func HostFn5[P1, P2, P3, P4, P5, R WasmInt](name string, fn func(context.Context, Module, P1, P2, P3, P4, P5) R) *HostModuleFunctionDefinition {
	return &HostModuleFunctionDefinition{
		Name:        name,
		ParamTypes:  []ValueType{wasmValueType[P1](), wasmValueType[P2](), wasmValueType[P3](), wasmValueType[P4](), wasmValueType[P5]()},
		ResultTypes: []ValueType{wasmValueType[R]()},
		Stack: func(ctx context.Context, m Module, s []uint64) {
			s[0] = uint64(fn(ctx, m, P1(s[0]), P2(s[1]), P3(s[2]), P4(s[3]), P5(s[4])))
		},
	}
}

func HostFn6[P1, P2, P3, P4, P5, P6, R WasmInt](name string, fn func(context.Context, Module, P1, P2, P3, P4, P5, P6) R) *HostModuleFunctionDefinition {
	return &HostModuleFunctionDefinition{
		Name:        name,
		ParamTypes:  []ValueType{wasmValueType[P1](), wasmValueType[P2](), wasmValueType[P3](), wasmValueType[P4](), wasmValueType[P5](), wasmValueType[P6]()},
		ResultTypes: []ValueType{wasmValueType[R]()},
		Stack: func(ctx context.Context, m Module, s []uint64) {
			s[0] = uint64(fn(ctx, m, P1(s[0]), P2(s[1]), P3(s[2]), P4(s[3]), P5(s[4]), P6(s[5])))
		},
	}
}

func HostFn7[P1, P2, P3, P4, P5, P6, P7, R WasmInt](name string, fn func(context.Context, Module, P1, P2, P3, P4, P5, P6, P7) R) *HostModuleFunctionDefinition {
	return &HostModuleFunctionDefinition{
		Name:        name,
		ParamTypes:  []ValueType{wasmValueType[P1](), wasmValueType[P2](), wasmValueType[P3](), wasmValueType[P4](), wasmValueType[P5](), wasmValueType[P6](), wasmValueType[P7]()},
		ResultTypes: []ValueType{wasmValueType[R]()},
		Stack: func(ctx context.Context, m Module, s []uint64) {
			s[0] = uint64(fn(ctx, m, P1(s[0]), P2(s[1]), P3(s[2]), P4(s[3]), P5(s[4]), P6(s[5]), P7(s[6])))
		},
	}
}

func HostFn8[P1, P2, P3, P4, P5, P6, P7, P8, R WasmInt](name string, fn func(context.Context, Module, P1, P2, P3, P4, P5, P6, P7, P8) R) *HostModuleFunctionDefinition {
	return &HostModuleFunctionDefinition{
		Name:        name,
		ParamTypes:  []ValueType{wasmValueType[P1](), wasmValueType[P2](), wasmValueType[P3](), wasmValueType[P4](), wasmValueType[P5](), wasmValueType[P6](), wasmValueType[P7](), wasmValueType[P8]()},
		ResultTypes: []ValueType{wasmValueType[R]()},
		Stack: func(ctx context.Context, m Module, s []uint64) {
			s[0] = uint64(fn(ctx, m, P1(s[0]), P2(s[1]), P3(s[2]), P4(s[3]), P5(s[4]), P6(s[5]), P7(s[6]), P8(s[7])))
		},
	}
}
