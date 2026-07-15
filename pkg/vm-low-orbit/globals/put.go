package globals

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) putGlobalValue(
	ctx context.Context,
	module common.Module,
	namePtr, nameSize,
	// TODO, maybe send type int here
	application, function,
	valuePtr, valueSize, valueCap uint32,
) uint32 {

	name, err0 := f.ReadString(module, namePtr, nameSize)
	if err0 != 0 {
		return uint32(err0)
	}

	value, err0 := f.ReadBytes(module, valuePtr, valueSize)
	if err0 != 0 {
		return uint32(err0)
	}

	path := f.getPath(application, function, name)

	db, err0 := f.kv()
	if err0 != 0 {
		return uint32(err0)
	}

	err := db.Put(ctx, path, value)
	if err != nil {
		return uint32(errno.ErrorDatabaseKeyNotFound)
	}

	return uint32(0)
}
