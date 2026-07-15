package database

import (
	"context"

	"github.com/taubyte/tau/core/vm"
)

func (d *Database) getDatabaseName(ctx context.Context, module vm.Module, resourceId uint32, dataPtr uint32) uint32 {
	db, err := d.GetCaller(resourceId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(d.WriteString(module, dataPtr, db.DBContext().Config.Name))
}

func (d *Database) getDatabaseNameSize(ctx context.Context, module vm.Module, resourceId uint32, sizePtr uint32) uint32 {
	db, err := d.GetCaller(resourceId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(d.WriteStringSize(module, sizePtr, db.DBContext().Config.Name))
}
