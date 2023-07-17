package database

import (
	"fmt"

	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
	iface "github.com/taubyte/go-interfaces/services/substrate/database"
	globalStream "github.com/taubyte/odo/protocols/node/components/database/globals/p2p/stream"
)

func New(srv nodeIface.Service, options ...Option) (service *Service, err error) {
	service = &Service{
		Service:   srv,
		databases: make(map[string]iface.Database),
		commits:   make(map[string]string, 0),
	}

	for _, opt := range options {
		if err := opt(service); err != nil {
			return nil, fmt.Errorf("running option %v failed with: %s", opt, err)
		}
	}

	// Attach to stream if present
	if service.globalStream, err = globalStream.Start(service); err != nil {
		return nil, fmt.Errorf("attaching global stream failed with: %s", err)
	}

	return
}
