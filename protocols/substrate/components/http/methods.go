package http

import (
	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
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
