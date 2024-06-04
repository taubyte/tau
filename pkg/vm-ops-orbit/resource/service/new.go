package service

import (
	service "github.com/taubyte/tau/core/services/substrate/components/p2p"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/common"
)

func New(f common.Factory) *Service {
	return &Service{
		Factory: f,
		callers: make(map[uint32]service.ServiceResource),
	}
}
