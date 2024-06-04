package service

import (
	"github.com/taubyte/go-sdk/errno"
	service "github.com/taubyte/tau/core/services/substrate/components/p2p"
)

func (f *Service) GetCaller(resourceId uint32) (service.ServiceResource, errno.Error) {
	resource, err := f.GetResource(resourceId)
	if err != 0 {
		return nil, err
	}

	f.callersLock.Lock()
	defer f.callersLock.Unlock()

	message, ok := f.callers[resourceId]
	if !ok {
		message, ok = resource.Caller.(service.ServiceResource)
		if !ok {
			return nil, errno.SmartOpErrorResourceNotFound
		}

		f.callers[resourceId] = message
	}

	return message, 0
}
