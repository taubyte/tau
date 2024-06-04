package function

import (
	"sync"

	"github.com/taubyte/tau/core/services/substrate/components/pubsub"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/common"
)

type FunctionPubSub struct {
	common.Factory

	callersLock sync.RWMutex
	callers     map[uint32]pubsub.Serviceable
}
