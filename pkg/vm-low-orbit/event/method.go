package event

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) getHttpEventMethodSize(ctx context.Context, module common.Module, eventId uint32, sizePtr uint32) uint32 {
	r, err := f.getEventRequest(eventId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteStringSize(module, sizePtr, r.Method))
}

func (f *Factory) getHttpEventMethod(ctx context.Context, module common.Module, eventId uint32, bufPtr uint32, bufSize uint32) uint32 {
	e, err := f.getEvent(eventId)
	if err != 0 {
		return uint32(err)
	}

	m := []byte(e.http.r.Method)
	if int(bufSize) != len(m) {
		return uint32(errno.ErrorBufferTooSmall)
	}

	return uint32(f.WriteBytes(module, bufPtr, m))
}
