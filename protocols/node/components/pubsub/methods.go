package pubsub

import (
	"context"

	iface "github.com/taubyte/go-interfaces/services/substrate/components"
)

func (s *Service) Close() error {
	s.cache.Close()
	return nil
}

func (s *Service) Cache() iface.Cache {
	return s.cache
}

// Used internally, not be confused with service config Dev
func (s *Service) Dev() bool {
	return s.dev
}

func (s *Service) Context() context.Context {
	return s.Node().Context()
}

func (s *Service) Verbose() bool {
	return s.verbose
}
