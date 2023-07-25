package smartOps

import (
	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
	"github.com/taubyte/odo/protocols/substrate/components/smartops/cache"
)

func New(srv nodeIface.Service, options ...Option) (*Service, error) {
	s := &Service{
		Service: srv,
		dev:     false,
		cache:   cache.New(srv.Node().Context()),
	}

	for _, opt := range options {
		err := opt(s)
		if err != nil {
			return nil, err
		}
	}

	return s, nil
}
