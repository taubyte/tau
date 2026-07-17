package services

import (
	"github.com/taubyte/tau/pkg/tcc/internal/parity/schema/basic"
	seer "github.com/taubyte/tau/pkg/tcc/internal/parity/yaseer"
)

func Open(seer *seer.Seer, name, application string) (Service, error) {
	service := &service{
		seer:        seer,
		name:        name,
		application: application,
	}

	var err error
	service.Resource, err = basic.New(seer, service, name)
	if err != nil {
		return nil, err
	}

	return service, nil
}
