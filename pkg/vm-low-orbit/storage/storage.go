package storage

import (
	"context"
	"fmt"
	"path"

	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/services/substrate/components/storage"
	common "github.com/taubyte/tau/core/vm"
	storageSpecs "github.com/taubyte/tau/pkg/specs/storage"
)

func (f *Factory) storageGet(ctx context.Context,
	module common.Module,
	storageMatchPtr, storageMatchSize,
	idPtr uint32,
) uint32 {
	storageMatch, err := f.ReadString(module, storageMatchPtr, storageMatchSize)
	if err != 0 {
		return uint32(err)
	}

	if open := f.openedStorage(storageMatch); open != nil {
		return uint32(f.WriteUint32Le(module, idPtr, open.id))
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
		return uint32(errno.ErrorStorageNotFound)
	}

	_storage := f.createStoragePointer(storage, storageMatch)

	return uint32(f.WriteUint32Le(module, idPtr, uint32(_storage.id)))
}

func (f *Factory) storageListFilesSize(ctx context.Context, module common.Module,
	storageId,
	sizePtr uint32,
) uint32 {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return uint32(err)
	}

	filePaths, err0 := storage.List(ctx, path.Join(storageSpecs.FilePath.String()))
	if err0 != nil {
		return uint32(errno.ErrorStorageListFailed)
	}

	return uint32(f.WriteStringSliceSizeFor(module, fmt.Sprintf("storage.list/%d", storageId), sizePtr, filePaths))
}

func (f *Factory) storageListFiles(ctx context.Context, module common.Module,
	storageId,
	sizePtr uint32,
) uint32 {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return uint32(err)
	}

	filePaths, err0 := storage.List(ctx, path.Join(storageSpecs.FilePath.String()))
	if err0 != nil {
		return uint32(errno.ErrorStorageListFailed)
	}

	return uint32(f.WriteStringSliceFor(module, fmt.Sprintf("storage.list/%d", storageId), sizePtr, filePaths))
}

func (f *Factory) createStoragePointer(storage storage.Storage, match string) *Storage {
	e := &Storage{
		Storage: storage,
		id:      f.generateStorageid(),
		files:   make(map[uint32]*File),
	}
	f.storagesLock.Lock()
	defer f.storagesLock.Unlock()
	f.storages[e.id] = e
	if f.byMatch == nil {
		f.byMatch = make(map[string]*Storage)
	}
	f.byMatch[match] = e

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

// openedStorage returns the handle already open for match, or nil.
func (f *Factory) openedStorage(match string) *Storage {
	f.storagesLock.RLock()
	defer f.storagesLock.RUnlock()
	return f.byMatch[match]
}
