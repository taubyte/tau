package kv

import (
	kvdb "github.com/taubyte/odo/pkgs/kvdb/database"
)

type kv struct {
	name     string
	database *kvdb.KVDatabase
	maxSize  uint64
}
