package http

import (
	"github.com/taubyte/go-interfaces/services/substrate"
	streams "github.com/taubyte/p2p/streams/service"
	"github.com/taubyte/tau/vm/cache"
)

type Service struct {
	substrate.Service
	cache       *cache.Cache
	dvPublicKey []byte
	stream      *streams.CommandService
}
