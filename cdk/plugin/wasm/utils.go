//go:build wasi || wasm
// +build wasi wasm

package main

import (
	"encoding/binary"
	"math"

	"github.com/extism/go-pdk"
)

func PdkOutput[T uint32 | uint64 | float32 | float64](val T) {
	switch v := any(val).(type) {
	case uint32:
		var data [4]byte
		binary.LittleEndian.PutUint32(data[:], v)
		pdk.Output(data[:])
	case uint64:
		var data [8]byte
		binary.LittleEndian.PutUint64(data[:], v)
		pdk.Output(data[:])
	case float32:
		var data [4]byte
		binary.LittleEndian.PutUint32(data[:], math.Float32bits(v))
		pdk.Output(data[:])
	case float64:
		var data [8]byte
		binary.LittleEndian.PutUint64(data[:], math.Float64bits(v))
		pdk.Output(data[:])
	default:
		panic("unsupported type")
	}
}
