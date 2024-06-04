package function

import (
	"sync"

	"github.com/taubyte/tau/core/services/substrate/components/p2p"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/common"
)

type FunctionP2P struct {
	common.Factory

	callersLock sync.RWMutex
	callers     map[uint32]p2p.Serviceable
}
