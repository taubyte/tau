package p2p

import (
	"context"

	nodeIface "github.com/taubyte/tau/core/services/substrate"
	p2pIface "github.com/taubyte/tau/core/services/substrate/components/p2p"
	"github.com/taubyte/tau/services/substrate/runtime/cache"
)

var _ p2pIface.Service = &Service{}

type Service struct {
	nodeIface.Service
	stream p2pIface.CommandService
	cache  *cache.Cache
}

func (s *Service) Close() error {
	s.cache.Close()
	s.stream.Close()
	return nil
}

func (s *Service) Context() context.Context {
	return s.Node().Context()
}
