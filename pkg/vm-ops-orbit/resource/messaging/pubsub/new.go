package messaging

import (
	messaging "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/common"
)

func New(f common.Factory) *MessagingPubSub {
	return &MessagingPubSub{
		Factory: f,
		callers: make(map[uint32]messaging.Channel),
	}
}
