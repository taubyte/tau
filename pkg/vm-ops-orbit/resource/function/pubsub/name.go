package function

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/vm"
)

func (f *FunctionPubSub) W_getFunctionPubSubName(ctx context.Context, module vm.Module, resourceId uint32, dataPtr uint32) errno.Error {
	_func, err := f.GetCaller(resourceId)
	if err != 0 {
		return err
	}

	return f.WriteString(module, dataPtr, _func.Config().Name)
}

func (f *FunctionPubSub) W_getFunctionPubSubNameSize(ctx context.Context, module vm.Module, resourceId uint32, sizePtr uint32) errno.Error {
	_func, err := f.GetCaller(resourceId)
	if err != 0 {
		return err
	}

	return f.WriteStringSize(module, sizePtr, _func.Config().Name)
}
