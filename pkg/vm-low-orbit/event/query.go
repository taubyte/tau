package event

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) getHttpEventQueryValueByNameSize(ctx context.Context, module common.Module, eventId uint32, sizePtr uint32, keyPtr uint32, keyLen uint32) uint32 {
	r, err := f.getEventRequest(eventId)
	if err != 0 {
		return uint32(err)
	}

	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return uint32(errno.ErrorAddressOutOfMemory)
	}

	return uint32(f.WriteStringSize(module, sizePtr, r.URL.Query().Get(key)))
}

func (f *Factory) getHttpEventQueryValueByName(ctx context.Context, module common.Module, eventId uint32, keyPtr uint32, keyLen uint32, bufPtr uint32, bufSize uint32) uint32 {
	r, err := f.getEventRequest(eventId)
	if err != 0 {
		return uint32(err)
	}

	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return uint32(errno.ErrorAddressOutOfMemory)
	}

	return uint32(f.WriteString(module, bufPtr, r.URL.Query().Get(key)))
}

func (f *Factory) getHttpEventRequestQueryKeysSize(ctx context.Context, module common.Module, eventId uint32, sizePtr uint32) uint32 {
	e, err := f.getEvent(eventId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteStringSliceSize(module, sizePtr, e.http.queryVars))
}

func (f *Factory) getHttpEventRequestQueryKeys(ctx context.Context, module common.Module, eventId uint32, bufPtr uint32) uint32 {
	e, err := f.getEvent(eventId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteStringSlice(module, bufPtr, e.http.queryVars))
}
