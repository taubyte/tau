package smartOps

import (
	"context"

	"github.com/taubyte/go-interfaces/services/substrate"
	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
)

var _ substrate.SmartOpsService = &Service{}

type Service struct {
	nodeIface.Service
	dev     bool
	verbose bool

	cache substrate.SmartOpsCache
}

func (s *Service) Close() error {
	s.cache.Close()
	return nil
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
