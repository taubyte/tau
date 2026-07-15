package event

import (
	"context"

	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) getHttpEventRequestHeaderKeysSize(ctx context.Context, module common.Module, eventId uint32, sizePtr uint32) uint32 {
	e, err := f.getEvent(eventId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteStringSliceSize(module, sizePtr, e.http.headerVars))
}

func (f *Factory) getHttpEventRequestHeaderKeys(ctx context.Context, module common.Module, eventId uint32, bufPtr uint32, keyIdx uint32) uint32 {
	e, err := f.getEvent(eventId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteStringSlice(module, bufPtr, e.http.headerVars))
}

func (f *Factory) eventHttpHeaderAdd(ctx context.Context, module common.Module, eventId uint32, keyPtr uint32, keyLen uint32, valPtr uint32, valLen uint32) uint32 {
	w, err := f.getEventWriter(eventId)
	if err != 0 {
		return uint32(err)
	}

	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return uint32(err)
	}

	value, err := f.ReadString(module, valPtr, valLen)
	if err != 0 {
		return uint32(err)
	}

	w.Header().Add(key, value)

	return 0
}

func (f *Factory) getHttpEventHeadersSize(ctx context.Context, module common.Module, eventId uint32, sizePtr uint32, keyPtr uint32, keyLen uint32) uint32 {
	r, err := f.getEventRequest(eventId)
	if err != 0 {
		return uint32(err)
	}

	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteStringSize(module, sizePtr, r.Header.Get(key)))
}

func (f *Factory) getHttpEventHeaders(ctx context.Context, module common.Module, eventId uint32, keyPtr uint32, keyLen uint32, bufPtr uint32, bufSize uint32) uint32 {
	r, err := f.getEventRequest(eventId)
	if err != 0 {
		return uint32(err)
	}

	key, err := f.ReadString(module, keyPtr, keyLen)

	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteString(module, bufPtr, r.Header.Get(key)))
}
