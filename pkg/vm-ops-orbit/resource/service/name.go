package service

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/vm"
)

func (f *Service) W_getServiceName(ctx context.Context, module vm.Module, resourceId uint32, dataPtr uint32) errno.Error {
	service, err := f.GetCaller(resourceId)
	if err != 0 {
		return err
	}

	return f.WriteString(module, dataPtr, service.Config().Name)
}

func (f *Service) W_getServiceNameSize(ctx context.Context, module vm.Module, resourceId uint32, sizePtr uint32) errno.Error {
	service, err := f.GetCaller(resourceId)
	if err != 0 {
		return err
	}

	return f.WriteStringSize(module, sizePtr, service.Config().Name)
}
