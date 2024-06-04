package event

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) W_getHttpEventRequestHeaderKeysSize(ctx context.Context, module common.Module, eventId uint32, sizePtr uint32) errno.Error {
	e, err := f.getEvent(eventId)
	if err != 0 {
		return err
	}

	return f.WriteStringSliceSize(module, sizePtr, e.http.headerVars)
}

func (f *Factory) W_getHttpEventRequestHeaderKeys(ctx context.Context, module common.Module, eventId uint32, bufPtr uint32, keyIdx uint32) errno.Error {
	e, err := f.getEvent(eventId)
	if err != 0 {
		return err
	}

	return f.WriteStringSlice(module, bufPtr, e.http.headerVars)
}

func (f *Factory) W_eventHttpHeaderAdd(ctx context.Context, module common.Module, eventId uint32, keyPtr uint32, keyLen uint32, valPtr uint32, valLen uint32) errno.Error {
	w, err := f.getEventWriter(eventId)
	if err != 0 {
		return err
	}

	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return err
	}

	value, err := f.ReadString(module, valPtr, valLen)
	if err != 0 {
		return err
	}

	w.Header().Add(key, value)

	return 0
}

func (f *Factory) W_getHttpEventHeadersSize(ctx context.Context, module common.Module, eventId uint32, sizePtr uint32, keyPtr uint32, keyLen uint32) errno.Error {
	r, err := f.getEventRequest(eventId)
	if err != 0 {
		return err
	}

	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return err
	}

	return f.WriteStringSize(module, sizePtr, r.Header.Get(key))
}

func (f *Factory) W_getHttpEventHeaders(ctx context.Context, module common.Module, eventId uint32, keyPtr uint32, keyLen uint32, bufPtr uint32, bufSize uint32) errno.Error {
	r, err := f.getEventRequest(eventId)
	if err != 0 {
		return err
	}

	key, err := f.ReadString(module, keyPtr, keyLen)

	if err != 0 {
		return err
	}

	return f.WriteString(module, bufPtr, r.Header.Get(key))
}
