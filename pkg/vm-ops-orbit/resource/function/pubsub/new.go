package function

import (
	"github.com/taubyte/tau/core/services/substrate/components/pubsub"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/common"
)

func New(f common.Factory) *FunctionPubSub {
	return &FunctionPubSub{
		Factory: f,
		callers: make(map[uint32]pubsub.Serviceable),
	}
}
