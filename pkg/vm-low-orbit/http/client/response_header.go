package client

import (
	"context"

	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) getHttpResponseHeaderSize(ctx context.Context, module common.Module,
	clientId,
	requestId,
	keyPtr, keyLen,
	sizePtr uint32,
) uint32 {
	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return uint32(err)
	}

	response, err := f.getResponse(clientId, requestId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteStringSliceSize(module, sizePtr, response.Header.Values(key)))
}

func (f *Factory) getHttpResponseHeader(ctx context.Context, module common.Module,
	clientId,
	requestId,
	keyPtr, keyLen,
	headerPtr uint32,
) uint32 {
	response, err := f.getResponse(clientId, requestId)
	if err != 0 {
		return uint32(err)
	}

	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteStringSlice(module, headerPtr, response.Header.Values(key)))
}

func (f *Factory) getHttpResponseHeaderKeysSize(ctx context.Context, module common.Module,
	clientId,
	requestId,
	sizePtr uint32,
) uint32 {
	response, err := f.getResponse(clientId, requestId)
	if err != 0 {
		return uint32(err)
	}

	var keys = make([]string, 0)
	for k := range response.Header {
		keys = append(keys, k)
	}

	return uint32(f.WriteStringSliceSize(module, sizePtr, keys))
}

func (f *Factory) getHttpResponseHeaderKeys(ctx context.Context, module common.Module,
	clientId,
	requestId,
	keysPtr, keysSize uint32,
) uint32 {
	response, err := f.getResponse(clientId, requestId)
	if err != 0 {
		return uint32(err)
	}

	var keys = make([]string, 0)
	for k := range response.Header {
		keys = append(keys, k)
	}

	return uint32(f.WriteStringSlice(module, keysPtr, keys))
}
