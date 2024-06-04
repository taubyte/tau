package event

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) W_getHttpEventPathSize(ctx context.Context, module common.Module, eventId uint32, sizePtr uint32) errno.Error {
	r, err := f.getEventRequest(eventId)
	if err != 0 {
		return err
	}

	return f.WriteStringSize(module, sizePtr, r.URL.Path)
}

func (f *Factory) W_getHttpEventPath(ctx context.Context, module common.Module, eventId uint32, bufPtr uint32, bufSize uint32) errno.Error {
	r, err := f.getEventRequest(eventId)
	if err != 0 {
		return err
	}

	return f.WriteString(module, bufPtr, r.URL.Path)
}
