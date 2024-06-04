package messaging

import (
	"sync"

	messaging "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/common"
)

type MessagingWebSocket struct {
	common.Factory

	callersLock sync.RWMutex
	callers     map[uint32]messaging.Messaging
}
