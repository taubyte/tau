package database

import (
	"fmt"

	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/database"
	globalStream "github.com/taubyte/odo/protocols/substrate/components/database/globals/p2p/stream"
)

func New(srv nodeIface.Service) (service *Service, err error) {
	service = &Service{
		Service:   srv,
		databases: make(map[string]iface.Database),
		commits:   make(map[string]string, 0),
	}

	// Attach to stream if present
	if service.globalStream, err = globalStream.Start(service); err != nil {
		return nil, fmt.Errorf("attaching global stream failed with: %s", err)
	}

	return
}
