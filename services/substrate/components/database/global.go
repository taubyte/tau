package database

import (
	iface "github.com/taubyte/tau/core/services/substrate/components/database"
	"github.com/taubyte/tau/services/substrate/components/database/common"
	globals "github.com/taubyte/tau/services/substrate/components/database/globals"
	mh "github.com/taubyte/utils/multihash"
)

func (s *Service) Global(projectID string) (db iface.Database, err error) {
	hash := mh.Hash("global" + projectID)

	var ok bool
	s.databasesLock.RLock()
	db, ok = s.databases[hash]
	s.databasesLock.RUnlock()
	if !ok {
		if db, err = globals.New(hash, common.Logger, s.DBFactory); err != nil {
			return nil, err
		}

		s.databasesLock.Lock()
		s.databases[hash] = db
		s.databasesLock.Unlock()
	}

	return
}
