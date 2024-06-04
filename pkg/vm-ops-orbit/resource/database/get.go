package database

import (
	"github.com/taubyte/tau/core/services/substrate/components/database"

	"github.com/taubyte/go-sdk/errno"
)

func (d *Database) GetCaller(resourceId uint32) (database.Database, errno.Error) {
	resource, err := d.GetResource(resourceId)
	if err != 0 {
		return nil, err
	}

	d.callersLock.Lock()
	defer d.callersLock.Unlock()

	db, ok := d.callers[resourceId]
	if !ok {
		db, ok = resource.Caller.(database.Database)
		if !ok {
			return nil, errno.SmartOpErrorResourceNotFound
		}

		d.callers[resourceId] = db
	}

	return db, 0
}
