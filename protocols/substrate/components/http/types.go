package http

import (
	"github.com/taubyte/go-interfaces/services/substrate"
	"github.com/taubyte/tau/vm/cache"
)

type Service struct {
	substrate.Service
	cache       *cache.Cache
	dvPublicKey []byte
}
