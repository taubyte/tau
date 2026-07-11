package database

import (
	"sync"

	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	nodeIface "github.com/taubyte/tau/core/services/substrate"
	iface "github.com/taubyte/tau/core/services/substrate/components/database"
)

var _ iface.Service = &Service{}

type Service struct {
	nodeIface.Service
	hoarderClient hoarderIface.Client
	databases     map[string]iface.Database
	commits       map[string]string
	databasesLock sync.RWMutex
	commitLock    sync.RWMutex
}

func (s *Service) Close() error {
	s.databasesLock.Lock()
	s.commitLock.Lock()

	s.databases = nil
	s.commits = nil

	s.databasesLock.Unlock()
	s.commitLock.Unlock()
	return nil
}
