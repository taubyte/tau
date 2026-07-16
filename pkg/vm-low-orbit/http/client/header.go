package client

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) setHttpRequestHeader(ctx context.Context, module common.Module,
	clientId,
	requestId,
	keyPtr, keyLen,
	valPtr, valLen uint32,
) uint32 {
	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return uint32(err)
	}

	values, err := f.ReadStringSlice(module, valPtr, valLen)
	if err != 0 {
		return uint32(err)
	}

	_, request, err := f.getClientAndRequest(clientId, requestId)
	if err != 0 {
		return uint32(err)
	}

	for idx, val := range values {
		if idx == 0 {
			request.Header.Set(key, val)
		} else {
			request.Header.Add(key, val)
		}
	}

	return uint32(0)
}

func (f *Factory) deleteHttpRequestHeader(ctx context.Context, module common.Module,
	clientId,
	requestId,
	keyPtr, keyLen uint32,
) uint32 {
	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return uint32(err)
	}

	_, request, err := f.getClientAndRequest(clientId, requestId)
	if err != 0 {
		return uint32(err)
	}

	request.Header.Del(key)
	if len(request.Header.Get(key)) != 0 {
		return uint32(errno.ErrorHeaderNotFound)
	}

	return uint32(0)
}

func (f *Factory) addHttpRequestHeader(
	ctx context.Context,
	module common.Module,
	clientId,
	requestId,
	keyPtr, keyLen,
	valPtr, valLen uint32,
) uint32 {
	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return uint32(err)
	}

	values, err := f.ReadStringSlice(module, valPtr, valLen)
	if err != 0 {
		return uint32(err)
	}

	_, request, err := f.getClientAndRequest(clientId, requestId)
	if err != 0 {
		return uint32(err)
	}

	for _, val := range values {
		request.Header.Add(key, val)
	}

	return uint32(0)
}

func (f *Factory) getHttpRequestHeaderSize(ctx context.Context, module common.Module,
	clientId,
	requestId,
	keyPtr, keyLen,
	sizePtr uint32,
) uint32 {
	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return uint32(err)
	}

	_, request, err := f.getClientAndRequest(clientId, requestId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteStringSliceSize(module, sizePtr, request.Header.Values(key)))
}

func (f *Factory) getHttpRequestHeader(ctx context.Context, module common.Module,
	clientId,
	requestId,
	keyPtr, keyLen,
	headerPtr uint32,
) uint32 {
	_, request, err := f.getClientAndRequest(clientId, requestId)
	if err != 0 {
		return uint32(err)
	}

	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteStringSlice(module, headerPtr, request.Header.Values(key)))
}

func (f *Factory) getHttpRequestHeaderKeysSize(ctx context.Context, module common.Module,
	clientId,
	requestId,
	sizePtr uint32,
) uint32 {
	_, request, err := f.getClientAndRequest(clientId, requestId)
	if err != 0 {
		return uint32(err)
	}
	var keys = make([]string, 0)
	for k := range request.Header {
		keys = append(keys, k)
	}

	return uint32(f.WriteStringSliceSize(module, sizePtr, keys))
}

func (f *Factory) getHttpRequestHeaderKeys(ctx context.Context, module common.Module,
	clientId,
	requestId,
	keysPtr, keysSize uint32,
) uint32 {
	_, request, err := f.getClientAndRequest(clientId, requestId)
	if err != 0 {
		return uint32(err)
	}

	var keys = make([]string, 0)
	for k := range request.Header {
		keys = append(keys, k)
	}

	return uint32(f.WriteStringSlice(module, keysPtr, keys))
}
