package service

import (
	"encoding/binary"
	"fmt"
	"math"
	"reflect"

	"github.com/taubyte/tau/core/vm"
	wasm "github.com/tetratelabs/wazero/api"
)

var _ vm.Return = &wasmReturn{}

func (r *wasmReturn) Error() error {
	return r.err
}

func (r *wasmReturn) Rets() []uint64 {
	return r.values
}

func (r *wasmReturn) Reflect(args ...interface{}) error {
	if r.err != nil {
		return r.err
	}

	if len(r.types) != len(args) {
		return fmt.Errorf("(%d) values returned, (%d) arguments passed", len(r.types), len(args))
	}

	for idx, arg := range args {
		valptr := reflect.ValueOf(arg)
		if valptr.Kind() != reflect.Ptr {
			return fmt.Errorf("need to pass a pointer")
		}
		val := valptr.Elem()
		switch val.Kind() {
		case reflect.Float32:
			if r.types[idx] != wasm.ValueTypeF64 && r.types[idx] != wasm.ValueTypeF32 {
				return fmt.Errorf("can not convert non float32 value to float32")
			}

			var b [8]byte
			binary.LittleEndian.PutUint64(b[:], r.values[idx])
			val.SetFloat(float64(math.Float32frombits(binary.LittleEndian.Uint32(b[0:4]))))
		case reflect.Float64:
			if r.types[idx] != wasm.ValueTypeF64 && r.types[idx] != wasm.ValueTypeF32 {
				return fmt.Errorf("can not convert `%s` non float64 value to float64", val.Kind().String())
			}

			val.SetFloat(math.Float64frombits(r.values[idx]))
		case reflect.Uint64, reflect.Uint32:
			if r.types[idx] != wasm.ValueTypeI64 && r.types[idx] != wasm.ValueTypeI32 {
				return fmt.Errorf("can not convert non uint value to uint")
			}

			val.SetUint(r.values[idx])
		case reflect.Int64, reflect.Int32:
			if r.types[idx] != wasm.ValueTypeI64 && r.types[idx] != wasm.ValueTypeI32 {
				return fmt.Errorf("can not convert non int value to int")
			}

			val.SetInt(int64(r.values[idx]))
		default:
			return fmt.Errorf("type `%T` is not supported ", val.Kind())
		}
	}

	return nil
}
