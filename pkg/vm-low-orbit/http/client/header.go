package client

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) W_setHttpRequestHeader(ctx context.Context, module common.Module,
	clientId,
	requestId,
	keyPtr, keyLen,
	valPtr, valLen uint32,
) (err errno.Error) {
	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return
	}

	values, err := f.ReadStringSlice(module, valPtr, valLen)
	if err != 0 {
		return
	}

	_, request, err := f.getClientAndRequest(clientId, requestId)
	if err != 0 {
		return
	}

	for idx, val := range values {
		if idx == 0 {
			request.Header.Set(key, val)
		} else {
			request.Header.Add(key, val)
		}
	}

	return 0
}

func (f *Factory) W_deleteHttpRequestHeader(ctx context.Context, module common.Module,
	clientId,
	requestId,
	keyPtr, keyLen uint32,
) (err errno.Error) {
	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return
	}

	_, request, err := f.getClientAndRequest(clientId, requestId)
	if err != 0 {
		return
	}

	request.Header.Del(key)
	if len(request.Header.Get(key)) != 0 {
		return errno.ErrorHeaderNotFound
	}

	return 0
}

func (f *Factory) W_addHttpRequestHeader(
	ctx context.Context,
	module common.Module,
	clientId,
	requestId,
	keyPtr, keyLen,
	valPtr, valLen uint32,
) (err errno.Error) {
	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return
	}

	values, err := f.ReadStringSlice(module, valPtr, valLen)
	if err != 0 {
		return
	}

	_, request, err := f.getClientAndRequest(clientId, requestId)
	if err != 0 {
		return
	}

	for _, val := range values {
		request.Header.Add(key, val)
	}

	return 0
}

func (f *Factory) W_getHttpRequestHeaderSize(ctx context.Context, module common.Module,
	clientId,
	requestId,
	keyPtr, keyLen,
	sizePtr uint32,
) errno.Error {
	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return err
	}

	_, request, err := f.getClientAndRequest(clientId, requestId)
	if err != 0 {
		return err
	}

	return f.WriteStringSliceSize(module, sizePtr, request.Header.Values(key))
}

func (f *Factory) W_getHttpRequestHeader(ctx context.Context, module common.Module,
	clientId,
	requestId,
	keyPtr, keyLen,
	headerPtr uint32,
) (err errno.Error) {
	_, request, err := f.getClientAndRequest(clientId, requestId)
	if err != 0 {
		return
	}

	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return
	}

	return f.WriteStringSlice(module, headerPtr, request.Header.Values(key))
}

func (f *Factory) W_getHttpRequestHeaderKeysSize(ctx context.Context, module common.Module,
	clientId,
	requestId,
	sizePtr uint32,
) errno.Error {
	_, request, err := f.getClientAndRequest(clientId, requestId)
	if err != 0 {
		return err
	}
	var keys = make([]string, 0)
	for k := range request.Header {
		keys = append(keys, k)
	}

	return f.WriteStringSliceSize(module, sizePtr, keys)
}

func (f *Factory) W_getHttpRequestHeaderKeys(ctx context.Context, module common.Module,
	clientId,
	requestId,
	keysPtr, keysSize uint32,
) errno.Error {
	_, request, err := f.getClientAndRequest(clientId, requestId)
	if err != 0 {
		return err
	}

	var keys = make([]string, 0)
	for k := range request.Header {
		keys = append(keys, k)
	}

	return f.WriteStringSlice(module, keysPtr, keys)
}
