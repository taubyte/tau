package client

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) readHttpResponseBody(ctx context.Context, module common.Module,
	clientId, requestId,
	bufPtr, bufSize,
	countPtr uint32,
) uint32 {
	response, err := f.getResponse(clientId, requestId)
	if err != 0 {
		return uint32(err)
	}

	_reader := response.Body.Read
	return uint32(f.Read(module, _reader, bufPtr, bufSize, countPtr))
}

func (f *Factory) closeHttpResponseBody(ctx context.Context, module common.Module,
	clientId, requestId uint32,
) uint32 {
	response, err := f.getResponse(clientId, requestId)
	if err != 0 {
		return uint32(err)
	}

	err0 := response.Body.Close()
	if err0 != nil {
		return uint32(errno.ErrorCloseBody)
	}

	return uint32(0)
}
