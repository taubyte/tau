package nodehttp

import (
	"context"

	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
)

func (s *Service) Close() error {
	s.cache.Close()
	return nil
}

func (s *Service) Context() context.Context {
	return s.Node().Context()
}

func (s *Service) Cache() commonIface.Cache {
	return s.cache
}
