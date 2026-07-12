package dfs

import "time"

var (
	Scheme     = "dfs"
	GetTimeout = 30 * time.Second
	CacheSize  = uint64(256 << 20) // bytes of decompressed wasm modules kept in memory
)
