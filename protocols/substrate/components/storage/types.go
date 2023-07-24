package substrate

import (
	"sync"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/go-interfaces/kvdb"
	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
	"github.com/taubyte/go-interfaces/services/substrate/components"
	storageIface "github.com/taubyte/go-interfaces/services/substrate/components/storage"
)

var _ components.ServiceComponent = &Service{}

type storageMethod func(storageIface.Service, storageIface.Context, log.StandardLogger, map[string]kvdb.KVDB) (storageIface.Storage, error)

type Service struct {
	nodeIface.Service

	dev           bool
	storages      map[string]storageIface.Storage
	storagesLock  sync.RWMutex
	storageMethod storageMethod
	matcherLock   sync.RWMutex
	matcher       map[string]kvdb.KVDB

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
	s.matcherLock.Lock()
	s.commitLock.Lock()

	s.storages = nil
	s.matcher = nil
	s.commits = nil

	s.storagesLock.Unlock()
	s.matcherLock.Unlock()
	s.commitLock.Unlock()

	return nil
}
