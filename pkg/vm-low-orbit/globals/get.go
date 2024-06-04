package globals

import (
	"context"
	"path"

	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/go-sdk/utils/slices"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) W_getGlobalValueSize(
	ctx context.Context,
	module common.Module,
	namePtr, nameSize,
	application, function,
	valueSizePtr uint32,
) errno.Error {
	name, err0 := f.ReadString(module, namePtr, nameSize)
	if err0 != 0 {
		return err0
	}

	db, err0 := f.kv()
	if err0 != 0 {
		return err0
	}

	prefix := f.getPathPrefix(application, function)
	keys, err := db.List(ctx, prefix)
	if err != nil {
		return errno.ErrorDatabaseListFailed
	}

	if !slices.Contains(keys, path.Join(prefix, name)) {
		return errno.ErrorDatabaseKeyNotFound
	}

	path := path.Join(prefix, name)
	value, err := db.Get(ctx, path)
	if err != nil {
		return errno.ErrorDatabaseGetFailed
	}

	return f.WriteUint32Le(module, valueSizePtr, uint32(len(value)))
}

func (f *Factory) W_getGlobalValue(
	ctx context.Context,
	module common.Module,
	namePtr, nameSize,
	application, function,
	valuePtr uint32,
) errno.Error {

	name, err0 := f.ReadString(module, namePtr, nameSize)
	if err0 != 0 {
		return err0
	}

	path := f.getPath(application, function, name)

	db, err0 := f.kv()
	if err0 != 0 {
		return err0
	}

	value, err := db.Get(ctx, path)
	if err != nil {
		return errno.ErrorDatabaseGetFailed
	}

	return f.WriteBytes(module, valuePtr, value)
}
