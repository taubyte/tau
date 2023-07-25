package p2p

import (
	iface "github.com/taubyte/go-interfaces/services/substrate/components"
)

func (s *Service) Cache() iface.Cache {
	return s.cache
}
