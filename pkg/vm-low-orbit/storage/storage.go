package storage

import (
	"context"
	"path"

	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/services/substrate/components/storage"
	common "github.com/taubyte/tau/core/vm"
	storageSpecs "github.com/taubyte/tau/pkg/specs/storage"
)

func (f *Factory) W_storageGet(ctx context.Context,
	module common.Module,
	storageMatchPtr, storageMatchSize,
	idPtr uint32,
) (err errno.Error) {
	storageMatch, err := f.ReadString(module, storageMatchPtr, storageMatchSize)
	if err != 0 {
		return
	}

	_ctx := f.parent.Context()
	storageContext := storage.Context{
		Context:       _ctx.Context(),
		ProjectId:     _ctx.Project(),
		ApplicationId: _ctx.Application(),
		Matcher:       storageMatch,
	}

	storage, err0 := f.storageNode.Get(storageContext)
	if err0 != nil {
		return errno.ErrorStorageNotFound
	}

	_storage := f.createStoragePointer(storage)

	return f.WriteUint32Le(module, idPtr, uint32(_storage.id))
}

func (f *Factory) W_storageListFilesSize(ctx context.Context, module common.Module,
	storageId,
	sizePtr uint32,
) (err errno.Error) {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return err
	}

	filePaths, err0 := storage.List(ctx, path.Join(storageSpecs.FilePath.String()))
	if err0 != nil {
		return errno.ErrorStorageListFailed
	}

	return f.WriteStringSliceSize(module, sizePtr, filePaths)
}

func (f *Factory) W_storageListFiles(ctx context.Context, module common.Module,
	storageId,
	sizePtr uint32,
) (err errno.Error) {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return err
	}

	filePaths, err0 := storage.List(ctx, path.Join(storageSpecs.FilePath.String()))
	if err0 != nil {
		return errno.ErrorStorageListFailed
	}

	return f.WriteStringSlice(module, sizePtr, filePaths)
}

func (f *Factory) createStoragePointer(storage storage.Storage) *Storage {
	e := &Storage{
		Storage: storage,
		id:      f.generateStorageid(),
		files:   make(map[uint32]*File),
	}
	f.storagesLock.Lock()
	defer f.storagesLock.Unlock()
	f.storages[e.id] = e

	return e
}

func (f *Factory) getStorage(storageId uint32) (*Storage, errno.Error) {
	f.storagesLock.RLock()
	defer f.storagesLock.RUnlock()
	if e, exists := f.storages[storageId]; exists {
		return e, 0
	}
	return nil, errno.ErrorEventNotFound
}

func (f *Factory) generateStorageid() uint32 {
	f.storagesLock.Lock()
	defer func() {
		f.storagesIdToGrab += 1
		f.storagesLock.Unlock()
	}()
	return f.storagesIdToGrab
}
