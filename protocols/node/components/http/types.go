package nodehttp

import (
	"bitbucket.org/taubyte/go-node-tvm/cache"
	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
	iface "github.com/taubyte/go-interfaces/services/substrate/http"
)

var _ iface.Service = &Service{}

type Service struct {
	nodeIface.Service
	dev         bool
	verbose     bool
	cache       *cache.Cache
	dvPublicKey []byte
}
