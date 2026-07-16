package service

import (
	"context"

	"github.com/taubyte/tau/core/vm"
)

func (f *Service) getServiceName(ctx context.Context, module vm.Module, resourceId uint32, dataPtr uint32) uint32 {
	service, err := f.GetCaller(resourceId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteString(module, dataPtr, service.Config().Name))
}

func (f *Service) getServiceNameSize(ctx context.Context, module vm.Module, resourceId uint32, sizePtr uint32) uint32 {
	service, err := f.GetCaller(resourceId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteStringSize(module, sizePtr, service.Config().Name))
}
