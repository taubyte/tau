package service

import (
	"sync"

	service "github.com/taubyte/tau/core/services/substrate/components/p2p"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/common"
)

type Service struct {
	common.Factory

	callersLock sync.RWMutex
	callers     map[uint32]service.ServiceResource
}
