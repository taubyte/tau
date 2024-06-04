package client

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) W_readHttpResponseBody(ctx context.Context, module common.Module,
	clientId, requestId,
	bufPtr, bufSize,
	countPtr uint32,
) (err errno.Error) {
	response, err := f.getResponse(clientId, requestId)
	if err != 0 {
		return
	}

	_reader := response.Body.Read
	return f.Read(module, _reader, bufPtr, bufSize, countPtr)
}

func (f *Factory) W_closeHttpResponseBody(ctx context.Context, module common.Module,
	clientId, requestId uint32,
) (err errno.Error) {
	response, err := f.getResponse(clientId, requestId)
	if err != 0 {
		return
	}

	err0 := response.Body.Close()
	if err0 != nil {
		return errno.ErrorCloseBody
	}

	return
}
