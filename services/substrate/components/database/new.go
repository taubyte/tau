package database

import (
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	nodeIface "github.com/taubyte/tau/core/services/substrate"
	iface "github.com/taubyte/tau/core/services/substrate/components/database"
)

func New(srv nodeIface.Service, hoarderClient hoarderIface.Client) (service *Service, err error) {
	service = &Service{
		Service:       srv,
		hoarderClient: hoarderClient,
		databases:     make(map[string]iface.Database),
		commits:       make(map[string]string, 0),
	}

	return
}
