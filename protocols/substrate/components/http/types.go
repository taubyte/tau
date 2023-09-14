package http

import (
	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
	"github.com/taubyte/tau/vm/cache"
)

// var _ iface.Service = &Service{}

type Service struct {
	nodeIface.Service
	cache       *cache.Cache
	dvPublicKey []byte
}
