package engine

import (
	"github.com/taubyte/tau/core/kvdb"
)

type Engine struct {
	prefix []string
	db     kvdb.KVDB
}
