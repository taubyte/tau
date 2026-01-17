//go:build web3
// +build web3

package client

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/ipfs/go-cid"
	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) W_ipfsNewContent(ctx context.Context, module common.Module,
	clientId,
	contentIdPtr uint32,
) errno.Error {
	client, err0 := f.getClient(clientId)
	if err0 != 0 {
		return err0
	}

	contentId := client.generateContentId()
	newFile, err := os.Create("tempFile" + fmt.Sprint("", contentId))
	if err != nil {
		return errno.ErrorCreatingNewFile
	}

	content := client.generateContent(contentId, cid.Cid{}, newFile)
	return f.WriteUint32Le(module, contentIdPtr, content.id)
}

func (f *Factory) W_ipfsOpenFile(ctx context.Context, module common.Module,
	clientId,
	contentIdPtr,
	cidPtr uint32,
) errno.Error {
	f.clientsLock.RLock()
	client, ok := f.clients[clientId]
	f.clientsLock.RUnlock()
	if !ok {
		return errno.ErrorClientNotFound
	}

	_cid, err0 := f.ReadCid(module, cidPtr)
	if err0 != 0 {
		return err0
	}

	file, err := f.ipfsNode.GetFile(f.ctx, _cid)
	if err != nil {
		return errno.ErrorCidNotFoundOnIpfs
	}

	content := client.generateContent(client.generateContentId(), _cid, file)
	return f.WriteUint32Le(module, contentIdPtr, content.id)
}

func (f *Factory) W_ipfsCloseFile(ctx context.Context, module common.Module,
	clientId,
	contentId uint32,
) errno.Error {
	_, content, err0 := f.getClientAndContent(clientId, contentId)
	if err0 != 0 {
		return err0
	}

	err := content.file.(io.Closer).Close()
	if err != nil {
		return errno.ErrorCloseFileFailed
	}

	return 0
}

func (f *Factory) W_ipfsFileCid(ctx context.Context, module common.Module,
	clientId,
	contentId,
	cidPtr uint32,
) errno.Error {
	_, content, err0 := f.getClientAndContent(clientId, contentId)
	if err0 != 0 {
		return err0
	}

	cid, err := cid.Parse(content.cid)
	if err != nil {
		return errno.ErrorInvalidCid
	}

	return f.WriteBytes(module, cidPtr, cid.Bytes())
}

func (f *Factory) W_ipfsWriteFile(ctx context.Context, module common.Module,
	clientId,
	contentId,
	buf, bufLen,
	writePtr uint32,
) errno.Error {
	data, ok := module.Memory().Read(buf, bufLen)
	if !ok {
		return errno.ErrorAddressOutOfMemory
	}

	f.clientsLock.RLock()
	client, ok := f.clients[clientId]
	f.clientsLock.RUnlock()
	if !ok {
		return errno.ErrorClientNotFound
	}

	content, err0 := client.getContent(contentId)
	if err0 != 0 {
		return err0
	}

	written, err := content.file.(io.Writer).Write(data)
	if err != nil {
		return errno.ErrorWritingFile
	}

	return f.WriteUint32Le(module, writePtr, uint32(written))
}

func (f *Factory) W_ipfsReadFile(ctx context.Context, module common.Module,
	clientId,
	contentId,
	buf, bufLen,
	countPtr uint32,
) errno.Error {
	_, content, err0 := f.getClientAndContent(clientId, contentId)
	if err0 != 0 {
		return err0
	}

	return f.Read(module, content.file.(io.Reader).Read, buf, bufLen, countPtr)
}

func (f *Factory) W_ipfsPushFile(ctx context.Context, module common.Module,
	clientId,
	contentId,
	cidPtr uint32,
) errno.Error {
	_, content, err0 := f.getClientAndContent(clientId, contentId)
	if err0 != 0 {
		return err0
	}

	file, ok := content.file.(io.ReadSeeker)
	if !ok {
		return errno.ErrorAddFileFailed
	}

	_, err := file.Seek(0, 0)
	if err != nil {
		return errno.ErrorAddFileFailed
	}

	_cid, err := f.ipfsNode.AddFile(file)
	if err != nil {
		return errno.ErrorAddFileFailed
	}

	return f.WriteCid(module, cidPtr, _cid)
}

func (f *Factory) W_ipfsSeekFile(ctx context.Context, module common.Module,
	clientId,
	contentId uint32,
	offset int64,
	whence,
	offsetPtr uint32,
) errno.Error {
	_, content, err0 := f.getClientAndContent(clientId, contentId)
	if err0 != 0 {
		return err0
	}

	if int(whence) > 2 || int(whence) < 0 {
		return errno.ErrorInvalidWhence
	}

	_offset, err := content.file.(io.Seeker).Seek(int64(offset), int(whence))
	if err != nil {
		return errno.ErrorSeekingFile
	}

	return f.WriteUint32Le(module, offsetPtr, uint32(_offset))
}
