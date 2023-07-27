package pubsub

import (
	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
	"github.com/taubyte/tau/protocols/substrate/components/pubsub/common"
	"github.com/taubyte/tau/vm/cache"
)

var _ common.LocalService = &Service{}

type Service struct {
	nodeIface.Service
	cache *cache.Cache
}
