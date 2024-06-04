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

func (f *Factory) W_storageAddFile(ctx context.Context, module common.Module,
	storageId,
	fileNamePtr, fileNameLen,
	versionPtr,
	bufPtr, bufLen uint32,
	overWrite uint32,
) (err errno.Error) {
	fileName, err := f.ReadString(module, fileNamePtr, fileNameLen)
	if err != 0 {
		return
	}

	file, ok := module.Memory().Read(bufPtr, bufLen)
	if !ok {
		return errno.ErrorAddressOutOfMemory
	}

	storage, err := f.getStorage(storageId)
	if err != 0 {
		return
	}

	version, err0 := storage.AddFile(ctx, bytes.NewReader(file), fileName, overWrite == 1)
	if err0 != nil {
		return errno.ErrorAddFileFailed
	}

	return f.WriteUint32Le(module, versionPtr, uint32(version))
}

func (f *Factory) W_storageGetFile(ctx context.Context,
	module common.Module,
	storageId,
	fileNamePtr, fileNameLen,
	version,
	fileIdPtr uint32,
) (err errno.Error) {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return
	}

	filename, err := f.ReadString(module, fileNamePtr, fileNameLen)
	if err != 0 {
		return
	}

	file := &File{
		id: storage.generateFileId(),
	}

	err = f.WriteUint32Le(module, fileIdPtr, file.id)
	if err != 0 {
		return
	}

	var err0 error

	meta, err0 := storage.Meta(ctx, filename, int(version))
	if err0 != nil {
		return errno.ErrorStorageGetMetaFailed
	}

	file.reader, err0 = meta.Get()
	if err0 != nil {
		return errno.ErrorGetFileFailed
	}

	file.cid = meta.Cid()
	file.version = meta.Version()

	return storage.setFile(file)
}

func (f *Factory) W_storageReadFile(ctx context.Context, module common.Module,
	storageId,
	fileId,
	bufPtr, bufSize,
	countPtr uint32,
) (err errno.Error) {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return
	}

	file, err := storage.getFile(fileId)
	if err != 0 {
		return
	}

	return f.Read(module, file.reader.Read, bufPtr, bufSize, countPtr)
}

func (f *Factory) W_storageCloseFile(ctx context.Context, module common.Module,
	storageId,
	fileId uint32,
) (err errno.Error) {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return
	}

	return storage.closeFile(fileId)
}

func (f *Factory) W_storageDeleteFile(ctx context.Context, module common.Module,
	storageId,
	fileNamePtr, fileNameLen,
	version,
	clear uint32,
) (err errno.Error) {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return
	}

	fileName, err := f.ReadString(module, fileNamePtr, fileNameLen)
	if err != 0 {
		return
	}
	versionInt := int(version)
	if clear == 1 {
		versionInt = -1
	}

	err0 := storage.DeleteFile(ctx, fileName, versionInt)
	if err0 != nil {
		return errno.ErrorDeleteFileFailed
	}

	return 0
}

func (f *Factory) W_storageListVersionsSize(ctx context.Context, module common.Module,
	storageId,
	fileNamePtr, fileNameLen,
	sizePtr uint32,
) (err errno.Error) {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return
	}

	fileName, err := f.ReadString(module, fileNamePtr, fileNameLen)
	if err != 0 {
		return
	}

	versions, err0 := storage.ListVersions(ctx, fileName)
	if err0 != nil {
		return errno.ErrorListFileVersionsFailed
	}

	return f.WriteStringSliceSize(module, sizePtr, versions)
}

func (f *Factory) W_storageListVersions(ctx context.Context, module common.Module,
	storageId,
	fileNamePtr, fileNameLen,
	versionsPtr uint32,
) (err errno.Error) {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return
	}

	fileName, err := f.ReadString(module, fileNamePtr, fileNameLen)
	if err != 0 {
		return
	}

	versions, err0 := storage.ListVersions(ctx, fileName)
	if err0 != nil {
		return errno.ErrorListFileVersionsFailed
	}

	return f.WriteStringSlice(module, versionsPtr, versions)
}

func (f *Factory) W_storageCid(ctx context.Context,
	module common.Module,
	storageId,
	fileNamePtr, fileNameLen,
	cidPtr uint32,
) errno.Error {
	storage, err0 := f.getStorage(storageId)
	if err0 != 0 {
		return errno.ErrorStorageNotFound
	}

	fileName, err0 := f.ReadString(module, fileNamePtr, fileNameLen)
	if err0 != 0 {
		return errno.ErrorAddressOutOfMemory
	}

	meta, err := storage.Meta(ctx, fileName, 0)
	if err != nil {
		return errno.ErrorStorageGetMetaFailed
	}

	return f.WriteCid(module, cidPtr, meta.Cid())
}

func (f *Factory) W_storageCurrentVersion(ctx context.Context, module common.Module,
	fileNamePtr, fileNameLen,
	versionPtr uint32,
) (err errno.Error) {
	fileName, err := f.ReadString(module, fileNamePtr, fileNameLen)
	if err != 0 {
		return errno.ErrorAddressOutOfMemory
	}

	f.versionLock.RLock()
	version, ok := f.version[fileName]
	f.versionLock.RUnlock()
	if !ok {
		return errno.ErrorAddressOutOfMemory
	}

	return f.WriteString(module, versionPtr, version)
}

func (f *Factory) W_storageCurrentVersionSize(ctx context.Context, module common.Module,
	storageId,
	fileNamePtr, fileNameLen,
	versionPtr uint32,
) (err errno.Error) {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return errno.ErrorAddressOutOfMemory
	}

	fileName, err := f.ReadString(module, fileNamePtr, fileNameLen)
	if err != 0 {
		return errno.ErrorAddressOutOfMemory
	}

	version, err0 := storage.GetLatestVersion(ctx, fileName)
	if err0 != nil {
		return errno.ErrorListFileVersionsFailed
	}

	f.versionLock.Lock()
	f.version[fileName] = strconv.Itoa(version)
	f.versionLock.Unlock()

	return f.WriteStringSize(module, versionPtr, strconv.Itoa(version))
}

func (f *Factory) W_storageUsedSize(ctx context.Context, module common.Module,
	storageId,
	sizePtr uint32,
) (err errno.Error) {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return
	}

	used, err0 := storage.Used(ctx)
	if err0 != nil {
		return errno.ErrorListingUsedSpaceFailed
	}

	return f.WriteStringSize(module, sizePtr, strconv.Itoa(used))
}

func (f *Factory) W_storageUsed(ctx context.Context, module common.Module,
	storageId,
	usedPtr uint32,
) (err errno.Error) {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return
	}

	used, err0 := storage.Used(ctx)
	if err0 != nil {
		return errno.ErrorListingUsedSpaceFailed
	}

	return f.WriteString(module, usedPtr, strconv.Itoa(used))
}

func (f *Factory) W_storageCapacitySize(ctx context.Context, module common.Module,
	storageId,
	sizePtr uint32,
) (err errno.Error) {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return
	}

	return f.WriteStringSize(module, sizePtr, strconv.Itoa(storage.Capacity()))
}

func (f *Factory) W_storageCapacity(ctx context.Context, module common.Module,
	storageId,
	capacityPtr uint32,
) (err errno.Error) {
	storage, err := f.getStorage(storageId)
	if err != 0 {
		return
	}

	return f.WriteString(module, capacityPtr, strconv.Itoa(storage.Capacity()))
}
