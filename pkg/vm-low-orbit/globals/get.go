package globals

import (
	"context"
	"path"

	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/go-sdk/utils/slices"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) getGlobalValueSize(
	ctx context.Context,
	module common.Module,
	namePtr, nameSize,
	application, function,
	valueSizePtr uint32,
) uint32 {
	name, err0 := f.ReadString(module, namePtr, nameSize)
	if err0 != 0 {
		return uint32(err0)
	}

	db, err0 := f.kv()
	if err0 != 0 {
		return uint32(err0)
	}

	prefix := f.getPathPrefix(application, function)
	keys, err := db.List(ctx, prefix)
	if err != nil {
		return uint32(errno.ErrorDatabaseListFailed)
	}

	if !slices.Contains(keys, path.Join(prefix, name)) {
		return uint32(errno.ErrorDatabaseKeyNotFound)
	}

	path := path.Join(prefix, name)
	value, err := db.Get(ctx, path)
	if err != nil {
		return uint32(errno.ErrorDatabaseGetFailed)
	}

	return uint32(f.WriteUint32Le(module, valueSizePtr, uint32(len(value))))
}

func (f *Factory) getGlobalValue(
	ctx context.Context,
	module common.Module,
	namePtr, nameSize,
	application, function,
	valuePtr uint32,
) uint32 {

	name, err0 := f.ReadString(module, namePtr, nameSize)
	if err0 != 0 {
		return uint32(err0)
	}

	path := f.getPath(application, function, name)

	db, err0 := f.kv()
	if err0 != 0 {
		return uint32(err0)
	}

	value, err := db.Get(ctx, path)
	if err != nil {
		return uint32(errno.ErrorDatabaseGetFailed)
	}

	return uint32(f.WriteBytes(module, valuePtr, value))
}
