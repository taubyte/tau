package nodehttp

import (
	"github.com/taubyte/odo/vm/cache"

	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
)

func New(srv nodeIface.Service, options ...Option) (*Service, error) {
	s := &Service{
		Service: srv,
		dev:     false,
		cache:   cache.New(),
	}

	for _, opt := range options {
		err := opt(s)
		if err != nil {
			return nil, err
		}
	}

	s.attach()

	return s, nil
}
