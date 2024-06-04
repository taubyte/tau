package function

import (
	"github.com/taubyte/go-sdk/errno"
	funcIface "github.com/taubyte/tau/core/services/substrate/components/http"
)

func (f *FunctionHttp) GetCaller(resourceId uint32) (funcIface.Function, errno.Error) {
	resource, err := f.GetResource(resourceId)
	if err != 0 {
		return nil, err
	}

	f.callersLock.Lock()
	defer f.callersLock.Unlock()

	_func, ok := f.callers[resourceId]
	if !ok {
		if _func, ok = resource.Caller.(funcIface.Function); !ok {
			return nil, errno.SmartOpErrorResourceNotFound
		}

		f.callers[resourceId] = _func
	}

	return _func, 0
}
