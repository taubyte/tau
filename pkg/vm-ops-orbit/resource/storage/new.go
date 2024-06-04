package storage

import (
	"github.com/taubyte/tau/core/services/substrate/components/storage"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/common"
)

func New(f common.Factory) *Storage {
	return &Storage{
		Factory: f,
		callers: make(map[uint32]storage.Storage),
	}
}
