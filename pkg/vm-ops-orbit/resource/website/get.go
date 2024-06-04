package website

import (
	"github.com/taubyte/go-sdk/errno"
	webIface "github.com/taubyte/tau/core/services/substrate/components/http"
)

func (d *Website) GetCaller(resourceId uint32) (webIface.Website, errno.Error) {
	resource, err := d.GetResource(resourceId)
	if err != 0 {
		return nil, err
	}

	d.callersLock.Lock()
	defer d.callersLock.Unlock()

	db, ok := d.callers[resourceId]
	if !ok {
		db, ok = resource.Caller.(webIface.Website)
		if !ok {
			return nil, errno.SmartOpErrorResourceNotFound
		}

		d.callers[resourceId] = db
	}

	return db, 0
}
