package http

import (
	commonIface "github.com/taubyte/tau/core/services/substrate/components"
)

func (s *Service) Close() error {
	if s.cache != nil {
		s.cache.Close()
	}

	return nil
}

func (s *Service) Cache() commonIface.Cache {
	return s.cache
}
