package database

import (
	"fmt"

	"github.com/taubyte/tau/core/kvdb"
	nodeIface "github.com/taubyte/tau/core/services/substrate"
	iface "github.com/taubyte/tau/core/services/substrate/components/database"
	globalStream "github.com/taubyte/tau/services/substrate/components/database/globals/p2p/stream"
)

func New(srv nodeIface.Service, factory kvdb.Factory) (service *Service, err error) {
	service = &Service{
		Service:   srv,
		DBFactory: factory,
		databases: make(map[string]iface.Database),
		commits:   make(map[string]string, 0),
	}

	// Attach to stream if present
	if service.globalStream, err = globalStream.Start(service); err != nil {
		return nil, fmt.Errorf("attaching global stream failed with: %s", err)
	}

	return
}
