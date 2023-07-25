package http

import (
	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/odo/vm/cache"

	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
)

var logger = log.Logger("substrate.service.http")

func New(srv nodeIface.Service, options ...Option) (*Service, error) {
	s := &Service{
		Service: srv,
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
