package nodehttp

import (
	"context"

	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
)

// Used internally, not be confused with service config Dev
func (s *Service) Dev() bool {
	return s.dev
}

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

/* Set with an option on creation of the node-http service */
func (s *Service) Verbose() bool {
	return s.verbose
}
