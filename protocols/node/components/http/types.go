package nodehttp

import (
	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
	iface "github.com/taubyte/go-interfaces/services/substrate/http"
	"github.com/taubyte/odo/vm/cache"
)

var _ iface.Service = &Service{}

type Service struct {
	nodeIface.Service
	dev         bool
	verbose     bool
	cache       *cache.Cache
	dvPublicKey []byte
}
