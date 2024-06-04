package storage

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/vm"
)

func (d *Storage) W_getStorageName(ctx context.Context, module vm.Module, resourceId uint32, dataPtr uint32) errno.Error {
	stg, err := d.GetCaller(resourceId)
	if err != 0 {
		return err
	}

	return d.WriteString(module, dataPtr, stg.Config().Name)
}

func (d *Storage) W_getStorageNameSize(ctx context.Context, module vm.Module, resourceId uint32, sizePtr uint32) errno.Error {
	stg, err := d.GetCaller(resourceId)
	if err != 0 {
		return err
	}

	return d.WriteStringSize(module, sizePtr, stg.Config().Name)
}
