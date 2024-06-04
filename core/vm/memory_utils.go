package vm

import "math"

// EncodeExternref encodes the input as a ValueTypeExternref.
func EncodeExternref(input uintptr) uint64 {
	return uint64(input)
}

// DecodeExternref decodes the input as a ValueTypeExternref.
func DecodeExternref(input uint64) uintptr {
	return uintptr(input)
}

// EncodeI32 encodes the input as a ValueTypeI32.
func EncodeI32(input int32) uint64 {
	return uint64(uint32(input))
}

// EncodeI64 encodes the input as a ValueTypeI64.
func EncodeI64(input int64) uint64 {
	return uint64(input)
}

// EncodeF32 encodes the input as a ValueTypeF32.
func EncodeF32(input float32) uint64 {
	return uint64(math.Float32bits(input))
}

// DecodeF32 decodes the input as a ValueTypeF32.
func DecodeF32(input uint64) float32 {
	return math.Float32frombits(uint32(input))
}

// EncodeF64 encodes the input as a ValueTypeF64.
func EncodeF64(input float64) uint64 {
	return math.Float64bits(input)
}

// DecodeF64 decodes the input as a ValueTypeF64.
func DecodeF64(input uint64) float64 {
	return math.Float64frombits(input)
}
