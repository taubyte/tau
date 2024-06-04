package event

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) W_getHttpEventQueryValueByNameSize(ctx context.Context, module common.Module, eventId uint32, sizePtr uint32, keyPtr uint32, keyLen uint32) errno.Error {
	r, err := f.getEventRequest(eventId)
	if err != 0 {
		return err
	}

	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return errno.ErrorAddressOutOfMemory
	}

	return f.WriteStringSize(module, sizePtr, r.URL.Query().Get(key))
}

func (f *Factory) W_getHttpEventQueryValueByName(ctx context.Context, module common.Module, eventId uint32, keyPtr uint32, keyLen uint32, bufPtr uint32, bufSize uint32) errno.Error {
	r, err := f.getEventRequest(eventId)
	if err != 0 {
		return err
	}

	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return errno.ErrorAddressOutOfMemory
	}

	return f.WriteString(module, bufPtr, r.URL.Query().Get(key))
}

func (f *Factory) W_getHttpEventRequestQueryKeysSize(ctx context.Context, module common.Module, eventId uint32, sizePtr uint32) errno.Error {
	e, err := f.getEvent(eventId)
	if err != 0 {
		return err
	}

	return f.WriteStringSliceSize(module, sizePtr, e.http.queryVars)
}

func (f *Factory) W_getHttpEventRequestQueryKeys(ctx context.Context, module common.Module, eventId uint32, bufPtr uint32) errno.Error {
	e, err := f.getEvent(eventId)
	if err != 0 {
		return err
	}

	return f.WriteStringSlice(module, bufPtr, e.http.queryVars)
}
