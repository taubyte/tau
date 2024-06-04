package function

import (
	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/services/substrate/components/pubsub"
)

func (f *FunctionPubSub) GetCaller(resourceId uint32) (pubsub.Serviceable, errno.Error) {
	resource, err := f.GetResource(resourceId)
	if err != 0 {
		return nil, err
	}

	f.callersLock.Lock()
	defer f.callersLock.Unlock()

	_func, ok := f.callers[resourceId]
	if !ok {
		_func, ok = resource.Caller.(pubsub.Serviceable)
		if !ok {
			return nil, errno.SmartOpErrorResourceNotFound
		}

		f.callers[resourceId] = _func
	}

	return _func, 0
}
