package pubsub

import (
	"bitbucket.org/taubyte/go-node-tvm/cache"
	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
	"github.com/taubyte/odo/protocols/node/components/pubsub/common"
)

var _ common.LocalService = &Service{}

type Service struct {
	nodeIface.Service
	dev     bool
	verbose bool
	cache   *cache.Cache
}
