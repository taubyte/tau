package storage

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/services/substrate/components/storage"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) storageNew(ctx context.Context, module common.Module,
	storageMatchPtr, storageMatchSize,
	idPtr uint32,
) uint32 {
	storageMatch, err := f.ReadString(module, storageMatchPtr, storageMatchSize)
	if err != 0 {
		return uint32(err)
	}

	_ctx := f.parent.Context()
	storageContext := storage.Context{
		ProjectId:     _ctx.Project(),
		ApplicationId: _ctx.Application(),
		Matcher:       storageMatch,
	}

	storage, err0 := f.storageNode.Storage(storageContext)
	if err0 != nil {
		return uint32(errno.ErrorDatabaseCreateFailed)
	}

	_storage := f.createStoragePointer(storage)

	return uint32(f.WriteUint32Le(module, idPtr, uint32(_storage.id)))
}
