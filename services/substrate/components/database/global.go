package database

import (
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	iface "github.com/taubyte/tau/core/services/substrate/components/database"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	dbpkg "github.com/taubyte/tau/services/substrate/components/database/database"
	kv "github.com/taubyte/tau/services/substrate/components/database/kv"
	mh "github.com/taubyte/tau/utils/multihash"
)

// globalSize is the fixed capacity of a project's global database.
const globalSize = uint64(1000000)

// Global returns the project-wide database, hosted on hoarders under the Global
// kind (no TNS validation). Substrate holds no data — it's a remote handle.
func (s *Service) Global(projectID string) (db iface.Database, err error) {
	hash := mh.Hash(projectID + "global")

	var ok bool
	s.databasesLock.RLock()
	db, ok = s.databases[hash]
	s.databasesLock.RUnlock()
	if ok {
		return db, nil
	}

	store, err := s.hoarderClient.KVDB(hoarderIface.Global, projectID, "", "global", "")
	if err != nil {
		return nil, err
	}

	dbContext := iface.Context{
		ProjectId: projectID,
		Matcher:   "global",
		Config:    &structureSpec.Database{Name: "global", Size: globalSize},
	}
	if db, err = dbpkg.Open(s, dbContext, kv.New(globalSize, "global", store)); err != nil {
		return nil, err
	}

	s.databasesLock.Lock()
	s.databases[hash] = db
	s.databasesLock.Unlock()

	return db, nil
}
