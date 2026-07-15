package globals

import (
	"context"
	"path"

	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/go-sdk/utils/slices"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) getOrCreateGlobalValueSize(
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

	path := path.Join(prefix, name)
	if !slices.Contains(keys, path) {
		err = db.Put(ctx, path, nil)
		if err != nil {
			return uint32(errno.ErrorDatabasePutFailed)
		}

		return uint32(0) // No need to write here, as it will always be 0
	}

	value, err := db.Get(ctx, path)
	if err != nil {
		return uint32(errno.ErrorDatabaseKeyNotFound)
	}

	return uint32(f.WriteUint32Le(module, valueSizePtr, uint32(len(value))))
}
