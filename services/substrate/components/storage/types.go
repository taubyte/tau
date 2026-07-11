package substrate

import (
	"sync"

	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	nodeIface "github.com/taubyte/tau/core/services/substrate"
	"github.com/taubyte/tau/core/services/substrate/components"
	storageIface "github.com/taubyte/tau/core/services/substrate/components/storage"
)

var _ components.ServiceComponent = &Service{}

type storageMethod func(storageIface.Service, hoarderIface.Client, storageIface.Context, string) (storageIface.Storage, error)

type Service struct {
	nodeIface.Service
	hoarderClient hoarderIface.Client
	storages      map[string]storageIface.Storage
	storagesLock  sync.RWMutex
	storageMethod storageMethod

	commitLock sync.RWMutex
	commits    map[string]string
}

func (s *Service) Cache() components.Cache {
	return nil
}

func (s *Service) CheckTns(components.MatchDefinition) ([]components.Serviceable, error) {
	return nil, nil
}

func (s *Service) Close() error {
	s.storagesLock.Lock()
	s.commitLock.Lock()

	s.storages = nil
	s.commits = nil

	s.storagesLock.Unlock()
	s.commitLock.Unlock()

	return nil
}
