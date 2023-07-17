package p2p

import (
	iface "github.com/taubyte/go-interfaces/services/substrate/common"
)

// Used internally, not be confused with service config Dev
func (s *Service) Dev() bool {
	return s.dev
}

func (s *Service) Verbose() bool {
	return s.verbose
}

func (s *Service) Cache() iface.Cache {
	return s.cache
}
