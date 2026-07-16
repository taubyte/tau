package storage

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/ipfs/go-cid"
	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) storageNewContent(ctx context.Context, module common.Module,
	contentIdPtr uint32,
) uint32 {
	f.contentLock.Lock()
	defer func() {
		f.contentIdToGrab += 1
		f.contentLock.Unlock()
	}()

	newFile, err := os.Create("tempFile" + fmt.Sprint("", f.contentIdToGrab))
	if err != nil {
		return uint32(errno.ErrorCreatingNewFile)
	}

	f.contents[f.contentIdToGrab] = &content{id: f.contentIdToGrab, cid: cid.Cid{}, file: newFile}
	return uint32(f.WriteUint32Le(module, contentIdPtr, f.contentIdToGrab))
}

func (f *Factory) storageOpenCid(ctx context.Context, module common.Module,
	contentIdPtr,
	cidPtr uint32,
) uint32 {
	cid, err0 := f.ReadCid(module, cidPtr)
	if err0 != 0 {
		return uint32(err0)
	}

	file, err := f.storageNode.GetFile(ctx, cid)
	if err != nil {
		return uint32(errno.ErrorCidNotFound)
	}

	newFile, err := os.Create(cid.String())
	if err != nil {
		return uint32(errno.ErrorCreatingNewFile)
	}

	_, err = file.WriteTo(newFile)
	if err != nil {
		return uint32(errno.ErrorWritingFile)
	}

	f.contentLock.Lock()
	defer func() {
		f.contentIdToGrab += 1
		f.contentLock.Unlock()
	}()

	f.contents[f.contentIdToGrab] = &content{id: f.contentIdToGrab, cid: cid, file: newFile}
	return uint32(f.WriteUint32Le(module, contentIdPtr, f.contentIdToGrab))
}

func (f *Factory) contentCloseFile(ctx context.Context,
	module common.Module,
	contentId uint32,
) uint32 {
	content, err0 := f.getContent(contentId)
	if err0 != 0 {
		return uint32(err0)
	}

	err := content.file.(io.Closer).Close()
	if err != nil {
		return uint32(errno.ErrorCloseFileFailed)
	}

	return 0
}

func (f *Factory) contentFileCid(ctx context.Context, module common.Module,
	contentId,
	cidPtr uint32,
) uint32 {
	content, err0 := f.getContent(contentId)
	if err0 != 0 {
		return uint32(err0)
	}

	return uint32(f.WriteCid(module, cidPtr, content.cid))
}

func (f *Factory) contentWriteFile(ctx context.Context, module common.Module,
	contentId,
	buf, bufLen,
	writePtr uint32,
) uint32 {
	data, ok := module.Memory().Read(buf, bufLen)
	if !ok {
		return uint32(errno.ErrorAddressOutOfMemory)
	}

	content, err0 := f.getContent(contentId)
	if err0 != 0 {
		return uint32(err0)
	}

	written, err := content.file.(io.Writer).Write(data)
	if err != nil {
		return uint32(errno.ErrorWritingFile)
	}

	return uint32(f.WriteUint32Le(module, writePtr, uint32(written)))
}

func (f *Factory) contentReadFile(ctx context.Context, module common.Module,
	contentId,
	buf, bufLen,
	countPtr uint32,
) uint32 {
	content, err0 := f.getContent(contentId)
	if err0 != 0 {
		return uint32(err0)
	}

	return uint32(f.Read(module, content.file.(io.Reader).Read, buf, bufLen, countPtr))
}

func (f *Factory) contentPushFile(ctx context.Context, module common.Module,
	contentId,
	cidPtr uint32,
) uint32 {
	content, err0 := f.getContent(contentId)
	if err0 != 0 {
		return uint32(err0)
	}

	file, ok := content.file.(io.ReadSeeker)
	if !ok {
		return uint32(errno.ErrorAddFileFailed)
	}

	_, err := file.Seek(0, 0)
	if err != nil {
		return uint32(errno.ErrorAddFileFailed)
	}

	_cid, err := f.storageNode.Add(file)
	if err != nil {
		return uint32(errno.ErrorAddFileFailed)
	}

	return uint32(f.WriteCid(module, cidPtr, _cid))
}

func (f *Factory) contentSeekFile(ctx context.Context, module common.Module,
	contentId uint32,
	offset int64,
	whence,
	offsetPtr uint32,
) uint32 {
	content, err0 := f.getContent(contentId)
	if err0 != 0 {
		return uint32(err0)
	}

	if int(whence) > 2 || int(whence) < 0 {
		return uint32(errno.ErrorInvalidWhence)
	}

	_offset, err := content.file.(io.Seeker).Seek(int64(offset), int(whence))
	if err != nil {
		return uint32(errno.ErrorSeekingFile)
	}

	return uint32(f.WriteUint32Le(module, offsetPtr, uint32(_offset)))
}
