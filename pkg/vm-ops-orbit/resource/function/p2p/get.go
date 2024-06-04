package function

import (
	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/services/substrate/components/p2p"
)

func (f *FunctionP2P) GetCaller(resourceId uint32) (p2p.Serviceable, errno.Error) {
	resource, err := f.GetResource(resourceId)
	if err != 0 {
		return nil, err
	}

	f.callersLock.Lock()
	defer f.callersLock.Unlock()

	_func, ok := f.callers[resourceId]
	if !ok {
		_func, ok = resource.Caller.(p2p.Serviceable)
		if !ok {
			return nil, errno.SmartOpErrorResourceNotFound
		}

		f.callers[resourceId] = _func
	}

	return _func, 0
}
