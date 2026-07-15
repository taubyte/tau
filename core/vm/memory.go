package vm

import api "github.com/samyfodil/wazy/api"

// Memory is a wazy module's memory, exposed directly (wazy is the only engine).
type Memory = api.Memory

// MemorySizer applies during compilation after a module has been decoded from wasm, but before it is instantiated.
type MemorySizer func(minPages uint32, maxPages *uint32) (min, capacity, max uint32)

const (
	// See https://www.w3.org/TR/2019/REC-wasm-core-1-20191205/#memory-instances%E2%91%A0
	MemoryPageSize = uint32(65536)
	// See https://www.w3.org/TR/2019/REC-wasm-core-1-20191205/#grow-mem
	MemoryLimitPages = uint32(65536)
)
