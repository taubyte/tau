package pubsub

import (
	nodeIface "github.com/taubyte/tau/core/services/substrate"
	pubsubIface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	"github.com/taubyte/tau/services/substrate/runtime/cache"
)

var _ pubsubIface.ServiceWithLookup = &Service{}

type Service struct {
	nodeIface.Service
	cache *cache.Cache
}
