package pubsub

import (
	nodeIface "github.com/taubyte/tau/core/services/substrate"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
	"github.com/taubyte/tau/services/substrate/runtime/cache"
)

var _ common.LocalService = &Service{}

type Service struct {
	nodeIface.Service
	cache *cache.Cache
}
