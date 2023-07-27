package pubsub

import (
	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
	"github.com/taubyte/tau/vm/cache"
)

func New(srv nodeIface.Service) (*Service, error) {
	s := &Service{
		Service: srv,
		cache:   cache.New(),
	}

	s.attach()

	return s, nil
}
