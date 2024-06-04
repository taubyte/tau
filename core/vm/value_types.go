package vm

import (
	"context"
	"fmt"
	"reflect"
)

// ExternType classifies imports and exports with their respective types.
//
// See https://www.w3.org/TR/2019/REC-wasm-core-1-20191205/#external-types%E2%91%A0
type ExternType = byte

const (
	ExternTypeFunc   ExternType = 0x00
	ExternTypeTable  ExternType = 0x01
	ExternTypeMemory ExternType = 0x02
	ExternTypeGlobal ExternType = 0x03
)

// The below are exported to consolidate parsing behavior for external types.
const (
	// ExternTypeFuncName is the name of the WebAssembly 1.0 (20191205) Text Format field for ExternTypeFunc.
	ExternTypeFuncName = "func"
	// ExternTypeTableName is the name of the WebAssembly 1.0 (20191205) Text Format field for ExternTypeTable.
	ExternTypeTableName = "table"
	// ExternTypeMemoryName is the name of the WebAssembly 1.0 (20191205) Text Format field for ExternTypeMemory.
	ExternTypeMemoryName = "memory"
	// ExternTypeGlobalName is the name of the WebAssembly 1.0 (20191205) Text Format field for ExternTypeGlobal.
	ExternTypeGlobalName = "global"
)

// ExternTypeName returns the name of the WebAssembly 1.0 (20191205) Text Format field of the given type.
func ExternTypeName(et ExternType) string {
	switch et {
	case ExternTypeFunc:
		return ExternTypeFuncName
	case ExternTypeTable:
		return ExternTypeTableName
	case ExternTypeMemory:
		return ExternTypeMemoryName
	case ExternTypeGlobal:
		return ExternTypeGlobalName
	}
	return fmt.Sprintf("%#x", et)
}

// ValueType describes a numeric type used in Web Assembly 1.0 (20191205).
type ValueType = byte

var (
	I32Type = reflect.TypeOf((*int32)(nil)).Elem()
	I64Type = reflect.TypeOf((*int64)(nil)).Elem()
	F32Type = reflect.TypeOf((*float32)(nil)).Elem()
	F64Type = reflect.TypeOf((*float64)(nil)).Elem()

	ContextType = reflect.TypeOf((*context.Context)(nil)).Elem()
	ModuleType  = reflect.TypeOf((*Module)(nil)).Elem()
)

func ValueTypeToReflectType(v ValueType) reflect.Type {
	switch v {
	case ValueTypeI32:
		return I32Type
	case ValueTypeI64:
		return I64Type
	case ValueTypeF32:
		return F32Type
	case ValueTypeF64:
		return F64Type
	default:
		return nil
	}
}

const (
	// ValueTypeI32 is a 32-bit integer.
	ValueTypeI32 ValueType = 0x7f
	// ValueTypeI64 is a 64-bit integer.
	ValueTypeI64 ValueType = 0x7e
	// ValueTypeF32 is a 32-bit floating point number.
	ValueTypeF32 ValueType = 0x7d
	// ValueTypeF64 is a 64-bit floating point number.
	ValueTypeF64 ValueType = 0x7c

	// ValueTypeExternref is a externref type.
	ValueTypeExternref ValueType = 0x6f
)

// ValueTypeName returns the type name of the given ValueType as a string.
// These type names match the names used in the WebAssembly text format.
func ValueTypeName(t ValueType) string {
	switch t {
	case ValueTypeI32:
		return "i32"
	case ValueTypeI64:
		return "i64"
	case ValueTypeF32:
		return "f32"
	case ValueTypeF64:
		return "f64"
	case ValueTypeExternref:
		return "externref"
	}
	return "unknown"
}
