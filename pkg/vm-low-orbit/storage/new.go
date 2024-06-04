package storage

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/services/substrate/components/storage"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) W_storageNew(ctx context.Context, module common.Module,
	storageMatchPtr, storageMatchSize,
	idPtr uint32,
) (err errno.Error) {
	storageMatch, err := f.ReadString(module, storageMatchPtr, storageMatchSize)
	if err != 0 {
		return
	}

	_ctx := f.parent.Context()
	storageContext := storage.Context{
		ProjectId:     _ctx.Project(),
		ApplicationId: _ctx.Application(),
		Matcher:       storageMatch,
	}

	storage, err0 := f.storageNode.Storage(storageContext)
	if err0 != nil {
		return errno.ErrorDatabaseCreateFailed
	}

	_storage := f.createStoragePointer(storage)

	return f.WriteUint32Le(module, idPtr, uint32(_storage.id))
}
