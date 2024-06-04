package pubsub

import (
	"context"

	iface "github.com/taubyte/tau/core/services/substrate/components"
)

func (s *Service) Close() error {
	s.cache.Close()
	return nil
}

func (s *Service) Cache() iface.Cache {
	return s.cache
}

func (s *Service) Context() context.Context {
	return s.Node().Context()
}
