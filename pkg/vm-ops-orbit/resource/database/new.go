package database

import (
	"github.com/taubyte/tau/core/services/substrate/components/database"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/common"
)

func New(f common.Factory) *Database {
	return &Database{
		Factory: f,
		callers: make(map[uint32]database.Database),
	}
}
