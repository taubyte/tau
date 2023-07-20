package p2p

import (
	"context"

	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
	p2pIface "github.com/taubyte/go-interfaces/services/substrate/components/p2p"
	"github.com/taubyte/odo/vm/cache"
)

var _ p2pIface.Service = &Service{}

type Service struct {
	nodeIface.Service
	stream  p2pIface.CommandService
	dev     bool
	verbose bool
	cache   *cache.Cache
}

func (s *Service) Close() error {
	s.cache.Close()
	s.stream.Close()
	return nil
}

func (s *Service) Context() context.Context {
	return s.Node().Context()
}
