package kv

import "github.com/taubyte/tau/core/kvdb"

type kv struct {
	name     string
	database kvdb.KVDB
	maxSize  uint64
}
