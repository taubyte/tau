package globals

import (
	"context"
	"path"

	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/go-sdk/utils/slices"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) W_getOrCreateGlobalValueSize(
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

	path := path.Join(prefix, name)
	if !slices.Contains(keys, path) {
		err = db.Put(ctx, path, nil)
		if err != nil {
			return errno.ErrorDatabasePutFailed
		}

		return 0 // No need to write here, as it will always be 0
	}

	value, err := db.Get(ctx, path)
	if err != nil {
		return errno.ErrorDatabaseKeyNotFound
	}

	return f.WriteUint32Le(module, valueSizePtr, uint32(len(value)))
}
