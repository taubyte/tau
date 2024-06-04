package client

import (
	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/services/substrate/components/database"
)

func (f *Factory) createDatabasePointer(database database.Database) *Database {
	e := &Database{
		Database: database,
		Id:       f.generateDatabaseId(),
	}

	f.databaseLock.Lock()
	defer f.databaseLock.Unlock()
	f.database[e.Id] = e

	return e
}

func (f *Factory) getDatabase(databaseId uint32) (*Database, errno.Error) {
	f.databaseLock.RLock()
	defer f.databaseLock.RUnlock()
	if e, exists := f.database[databaseId]; exists {
		if e.KV() == nil {
			return nil, errno.ErrorKeystoreNotFound
		}

		return e, 0
	}

	return nil, errno.ErrorDatabaseNotFound
}

func (f *Factory) generateDatabaseId() uint32 {
	f.databaseLock.Lock()
	defer func() {
		f.databaseIdToGrab += 1
		f.databaseLock.Unlock()
	}()

	return f.databaseIdToGrab

}
