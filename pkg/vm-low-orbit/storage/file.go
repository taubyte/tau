package storage

import (
	"bytes"
	"context"
	"strconv"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (st *Storage) generateFileId() uint32 {
	st.fileLock.Lock()
	defer func() {
		st.fileIdToGrab += 1
		st.fileLock.Unlock()
	}()

	return st.fileIdToGrab
}

func (st *Storage) getFile(fileId uint32) (*File, errno.Error) {
	st.fileLock.RLock()

	file, ok := st.files[fileId]
	st.fileLock.RUnlock()
	if !ok {
		return nil, errno.ErrorAddressOutOfMemory
	}

	return file, 0
}

func (st *Storage) setFile(file *File) errno.Error {
	st.fileLock.Lock()
	defer st.fileLock.Unlock()

	st.files[file.id] = file

	return 0
}

func (st *Storage) closeFile(fileId uint32) errno.Error {
	st.fileLock.Lock()
	defer st.fileLock.Unlock()

	file, ok := st.files[fileId]
	if !ok {
		return errno.ErrorAddressOutOfMemory
	}

	err := file.reader.Close()
	if err != nil {
		return errno.ErrorCloseFileFailed
	}

	delete(st.files, fileId)

	return 0
}

func (f *Factory) storageAddFile(ctx context.Context, module common.Module,
	storageId,
	fileNamePtr, fileNameLen,
	versionPtr,
	bufPtr, bufLen uint32,
	overWrite uint32,
) uint32 {
	fileName, err := f.ReadString(module, fileNamePtr, fileNameLen)
	if err != 0 {
		return uint32(err)
	}

	file, ok := module.Memory().Read(bufPtr, bufLen)
	if !ok {
		return uint32(errno.ErrorAddressOutOfMemory)
	}

	storage, err := f.getStorage(storageId)
	if err != 0 {
		return uint32(err)
	}

	version, err0 := storage.AddFile(ctx, bytes.NewReader(file), fileName, overWrite == 1)
	if err0 != nil {
		return uint32(errno.ErrorAddFileFailed)
	}

	return uint32(f.WriteUint32Le(module, versionPtr, uint32(version)))
}

func (f *Factory) storageGetFile(ctx context.Context,
	module common.Module,
	storageId,
	fileNamePtr, fileNameLen,
	version,
	fileIdPtr uint32,
) uint32 {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return uint32(err)
	}

	filename, err := f.ReadString(module, fileNamePtr, fileNameLen)
	if err != 0 {
		return uint32(err)
	}

	file := &File{
		id: storage.generateFileId(),
	}

	err = f.WriteUint32Le(module, fileIdPtr, file.id)
	if err != 0 {
		return uint32(err)
	}

	var err0 error

	meta, err0 := storage.Meta(ctx, filename, int(version))
	if err0 != nil {
		return uint32(errno.ErrorStorageGetMetaFailed)
	}

	file.reader, err0 = meta.Get()
	if err0 != nil {
		return uint32(errno.ErrorGetFileFailed)
	}

	file.cid = meta.Cid()
	file.version = meta.Version()

	return uint32(storage.setFile(file))
}

func (f *Factory) storageReadFile(ctx context.Context, module common.Module,
	storageId,
	fileId,
	bufPtr, bufSize,
	countPtr uint32,
) uint32 {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return uint32(err)
	}

	file, err := storage.getFile(fileId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.Read(module, file.reader.Read, bufPtr, bufSize, countPtr))
}

func (f *Factory) storageCloseFile(ctx context.Context, module common.Module,
	storageId,
	fileId uint32,
) uint32 {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(storage.closeFile(fileId))
}

func (f *Factory) storageDeleteFile(ctx context.Context, module common.Module,
	storageId,
	fileNamePtr, fileNameLen,
	version,
	clear uint32,
) uint32 {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return uint32(err)
	}

	fileName, err := f.ReadString(module, fileNamePtr, fileNameLen)
	if err != 0 {
		return uint32(err)
	}
	versionInt := int(version)
	if clear == 1 {
		versionInt = -1
	}

	err0 := storage.DeleteFile(ctx, fileName, versionInt)
	if err0 != nil {
		return uint32(errno.ErrorDeleteFileFailed)
	}

	return 0
}

func (f *Factory) storageListVersionsSize(ctx context.Context, module common.Module,
	storageId,
	fileNamePtr, fileNameLen,
	sizePtr uint32,
) uint32 {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return uint32(err)
	}

	fileName, err := f.ReadString(module, fileNamePtr, fileNameLen)
	if err != 0 {
		return uint32(err)
	}

	versions, err0 := storage.ListVersions(ctx, fileName)
	if err0 != nil {
		return uint32(errno.ErrorListFileVersionsFailed)
	}

	return uint32(f.WriteStringSliceSize(module, sizePtr, versions))
}

func (f *Factory) storageListVersions(ctx context.Context, module common.Module,
	storageId,
	fileNamePtr, fileNameLen,
	versionsPtr uint32,
) uint32 {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return uint32(err)
	}

	fileName, err := f.ReadString(module, fileNamePtr, fileNameLen)
	if err != 0 {
		return uint32(err)
	}

	versions, err0 := storage.ListVersions(ctx, fileName)
	if err0 != nil {
		return uint32(errno.ErrorListFileVersionsFailed)
	}

	return uint32(f.WriteStringSlice(module, versionsPtr, versions))
}

func (f *Factory) storageCid(ctx context.Context,
	module common.Module,
	storageId,
	fileNamePtr, fileNameLen,
	cidPtr uint32,
) uint32 {
	storage, err0 := f.getStorage(storageId)
	if err0 != 0 {
		return uint32(errno.ErrorStorageNotFound)
	}

	fileName, err0 := f.ReadString(module, fileNamePtr, fileNameLen)
	if err0 != 0 {
		return uint32(errno.ErrorAddressOutOfMemory)
	}

	meta, err := storage.Meta(ctx, fileName, 0)
	if err != nil {
		return uint32(errno.ErrorStorageGetMetaFailed)
	}

	return uint32(f.WriteCid(module, cidPtr, meta.Cid()))
}

func (f *Factory) storageCurrentVersion(ctx context.Context, module common.Module,
	fileNamePtr, fileNameLen,
	versionPtr uint32,
) uint32 {
	fileName, err := f.ReadString(module, fileNamePtr, fileNameLen)
	if err != 0 {
		return uint32(errno.ErrorAddressOutOfMemory)
	}

	f.versionLock.RLock()
	version, ok := f.version[fileName]
	f.versionLock.RUnlock()
	if !ok {
		return uint32(errno.ErrorAddressOutOfMemory)
	}

	return uint32(f.WriteString(module, versionPtr, version))
}

func (f *Factory) storageCurrentVersionSize(ctx context.Context, module common.Module,
	storageId,
	fileNamePtr, fileNameLen,
	versionPtr uint32,
) uint32 {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return uint32(errno.ErrorAddressOutOfMemory)
	}

	fileName, err := f.ReadString(module, fileNamePtr, fileNameLen)
	if err != 0 {
		return uint32(errno.ErrorAddressOutOfMemory)
	}

	version, err0 := storage.GetLatestVersion(ctx, fileName)
	if err0 != nil {
		return uint32(errno.ErrorListFileVersionsFailed)
	}

	f.versionLock.Lock()
	f.version[fileName] = strconv.Itoa(version)
	f.versionLock.Unlock()

	return uint32(f.WriteStringSize(module, versionPtr, strconv.Itoa(version)))
}

func (f *Factory) storageUsedSize(ctx context.Context, module common.Module,
	storageId,
	sizePtr uint32,
) uint32 {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return uint32(err)
	}

	used, err0 := storage.Used(ctx)
	if err0 != nil {
		return uint32(errno.ErrorListingUsedSpaceFailed)
	}

	return uint32(f.WriteStringSize(module, sizePtr, strconv.Itoa(used)))
}

func (f *Factory) storageUsed(ctx context.Context, module common.Module,
	storageId,
	usedPtr uint32,
) uint32 {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return uint32(err)
	}

	used, err0 := storage.Used(ctx)
	if err0 != nil {
		return uint32(errno.ErrorListingUsedSpaceFailed)
	}

	return uint32(f.WriteString(module, usedPtr, strconv.Itoa(used)))
}

func (f *Factory) storageCapacitySize(ctx context.Context, module common.Module,
	storageId,
	sizePtr uint32,
) uint32 {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteStringSize(module, sizePtr, strconv.Itoa(storage.Capacity())))
}

func (f *Factory) storageCapacity(ctx context.Context, module common.Module,
	storageId,
	capacityPtr uint32,
) uint32 {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteString(module, capacityPtr, strconv.Itoa(storage.Capacity())))
}
