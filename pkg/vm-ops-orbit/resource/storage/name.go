package storage

import (
	"context"

	"github.com/taubyte/tau/core/vm"
)

func (d *Storage) getStorageName(ctx context.Context, module vm.Module, resourceId uint32, dataPtr uint32) uint32 {
	stg, err := d.GetCaller(resourceId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(d.WriteString(module, dataPtr, stg.Config().Name))
}

func (d *Storage) getStorageNameSize(ctx context.Context, module vm.Module, resourceId uint32, sizePtr uint32) uint32 {
	stg, err := d.GetCaller(resourceId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(d.WriteStringSize(module, sizePtr, stg.Config().Name))
}
