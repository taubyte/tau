package function

import (
	"context"

	"github.com/taubyte/tau/core/vm"
)

func (f *FunctionHttp) getFunctionHttpName(ctx context.Context, module vm.Module, resourceId uint32, dataPtr uint32) uint32 {
	_func, err := f.GetCaller(resourceId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteString(module, dataPtr, _func.Config().Name))
}

func (f *FunctionHttp) getFunctionHttpNameSize(ctx context.Context, module vm.Module, resourceId uint32, sizePtr uint32) uint32 {
	_func, err := f.GetCaller(resourceId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteStringSize(module, sizePtr, _func.Config().Name))
}
