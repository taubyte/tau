package event

import (
	"context"

	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) getHttpEventHostSize(ctx context.Context, module common.Module, eventId uint32, sizePtr uint32) uint32 {
	r, err := f.getEventRequest(eventId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteStringSize(module, sizePtr, r.Host))
}

func (f *Factory) getHttpEventHost(ctx context.Context, module common.Module, eventId uint32, bufPtr uint32, bufSize uint32) uint32 {
	r, err := f.getEventRequest(eventId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteString(module, bufPtr, r.Host))
}
