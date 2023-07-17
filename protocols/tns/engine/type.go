package engine

import (
	"github.com/taubyte/go-interfaces/kvdb"
)

type Engine struct {
	prefix []string
	db     kvdb.KVDB
}
