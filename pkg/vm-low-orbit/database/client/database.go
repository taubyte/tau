package client

import (
	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/services/substrate/components/database"
)

func (f *Factory) createDatabasePointer(database database.Database, match string) *Database {
	e := &Database{
		Database: database,
		Id:       f.generateDatabaseId(),
		match:    match,
		refs:     1,
	}

	f.databaseLock.Lock()
	defer f.databaseLock.Unlock()
	f.database[e.Id] = e
	if f.byMatch == nil {
		f.byMatch = make(map[string]*Database)
	}
	f.byMatch[match] = e

	return e
}

// openedDatabase returns the handle already open for match, counting the new
// open, or nil when there is none.
func (f *Factory) openedDatabase(match string) *Database {
	f.databaseLock.Lock()
	defer f.databaseLock.Unlock()
	e, ok := f.byMatch[match]
	if !ok {
		return nil
	}
	e.refs++
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

// releaseDatabase counts one close of e. The handle is shared by every open of
// the same database, so only the last close removes and closes it; it reports
// whether that happened.
func (f *Factory) releaseDatabase(e *Database) bool {
	f.databaseLock.Lock()
	defer f.databaseLock.Unlock()
	if e.refs--; e.refs > 0 {
		return false
	}
	delete(f.database, e.Id)
	f.dropBatches(e)
	if f.byMatch[e.match] == e {
		delete(f.byMatch, e.match)
	}
	e.Close()
	return true
}
