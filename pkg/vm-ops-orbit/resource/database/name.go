package database

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/vm"
)

func (d *Database) W_getDatabaseName(ctx context.Context, module vm.Module, resourceId uint32, dataPtr uint32) errno.Error {
	db, err := d.GetCaller(resourceId)
	if err != 0 {
		return err
	}

	return d.WriteString(module, dataPtr, db.DBContext().Config.Name)
}

func (d *Database) W_getDatabaseNameSize(ctx context.Context, module vm.Module, resourceId uint32, sizePtr uint32) errno.Error {
	db, err := d.GetCaller(resourceId)
	if err != 0 {
		return err
	}

	return d.WriteStringSize(module, sizePtr, db.DBContext().Config.Name)
}
