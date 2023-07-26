package kv

import "github.com/taubyte/go-interfaces/kvdb"

type kv struct {
	name     string
	database kvdb.KVDB
	maxSize  uint64
}
