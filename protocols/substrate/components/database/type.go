package database

import (
	"sync"

	"github.com/taubyte/go-interfaces/kvdb"
	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/database"
	globalStream "github.com/taubyte/tau/protocols/substrate/components/database/globals/p2p/stream"
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
