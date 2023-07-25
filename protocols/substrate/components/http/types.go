package http

import (
	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/http"
	"github.com/taubyte/odo/vm/cache"
)

var _ iface.Service = &Service{}

type Service struct {
	nodeIface.Service
	cache       *cache.Cache
	dvPublicKey []byte
}
