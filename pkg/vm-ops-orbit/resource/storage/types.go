package storage

import (
	"sync"

	"github.com/taubyte/tau/core/services/substrate/components/storage"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/common"
)

type Storage struct {
	common.Factory

	callersLock sync.RWMutex
	callers     map[uint32]storage.Storage
}
