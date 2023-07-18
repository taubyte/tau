package pubsub

import (
	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
	"github.com/taubyte/odo/protocols/node/components/pubsub/common"
	"github.com/taubyte/odo/vm/cache"
)

var _ common.LocalService = &Service{}

type Service struct {
	nodeIface.Service
	dev     bool
	verbose bool
	cache   *cache.Cache
}
