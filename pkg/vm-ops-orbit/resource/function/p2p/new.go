package function

import (
	"github.com/taubyte/tau/core/services/substrate/components/p2p"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/common"
)

func New(f common.Factory) *FunctionP2P {
	return &FunctionP2P{
		Factory: f,
		callers: make(map[uint32]p2p.Serviceable),
	}
}
