package database

import (
	"sync"

	"github.com/taubyte/tau/core/services/substrate/components/database"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/common"
)

type Database struct {
	common.Factory

	callersLock sync.RWMutex
	callers     map[uint32]database.Database
}
