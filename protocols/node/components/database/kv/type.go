package kv

import (
	kvdb "bitbucket.org/taubyte/kvdb/database"
)

type kv struct {
	name     string
	database *kvdb.KVDatabase
	maxSize  uint64
}
