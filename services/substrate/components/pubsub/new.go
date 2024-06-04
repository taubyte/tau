package pubsub

import (
	nodeIface "github.com/taubyte/tau/core/services/substrate"
	"github.com/taubyte/tau/services/substrate/runtime/cache"
)

func New(srv nodeIface.Service) (*Service, error) {
	s := &Service{
		Service: srv,
		cache:   cache.New(),
	}

	s.attach()

	return s, nil
}
