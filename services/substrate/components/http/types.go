package http

import (
	"github.com/taubyte/tau/core/services/substrate"
	"github.com/taubyte/tau/pkg/config"
	"github.com/taubyte/tau/services/substrate/runtime/cache"
)

type Service struct {
	substrate.Service
	config      config.Config
	cache       *cache.Cache
	dvPublicKey []byte
}
