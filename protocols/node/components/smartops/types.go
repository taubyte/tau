package smartOps

import (
	"context"

	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
	"github.com/taubyte/go-interfaces/services/substrate/counters"
	iface "github.com/taubyte/go-interfaces/services/substrate/smartops"
)

var _ iface.Service = &Service{}

type Service struct {
	nodeIface.Service
	dev     bool
	verbose bool

	cache iface.Cache
}

func (s *Service) Close() error {
	s.cache.Close()
	return nil
}

func (s *Service) Counter() counters.Service {
	return s.Service.Counter()
}

func (s *Service) Context() context.Context {
	return s.Node().Context()
}

func (s *Service) Dev() bool {
	return s.dev
}

func (s *Service) Verbose() bool {
	return s.verbose
}
