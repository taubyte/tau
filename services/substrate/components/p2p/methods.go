package p2p

import (
	iface "github.com/taubyte/tau/core/services/substrate/components"
)

func (s *Service) Cache() iface.Cache {
	return s.cache
}
