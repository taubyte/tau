package database

import (
	"sync"

	"github.com/taubyte/tau/core/kvdb"
	nodeIface "github.com/taubyte/tau/core/services/substrate"
	iface "github.com/taubyte/tau/core/services/substrate/components/database"
	globalStream "github.com/taubyte/tau/services/substrate/components/database/globals/p2p/stream"
)

var _ iface.Service = &Service{}

type Service struct {
	nodeIface.Service
	DBFactory     kvdb.Factory
	databases     map[string]iface.Database
	commits       map[string]string
	databasesLock sync.RWMutex
	commitLock    sync.RWMutex

	globalStream *globalStream.StreamHandler
}

func (s *Service) Close() error {
	s.databasesLock.Lock()
	s.commitLock.Lock()

	s.databases = nil
	s.commits = nil
	s.globalStream.Close()

	s.databasesLock.Unlock()
	s.commitLock.Unlock()
	return nil
}
