package event

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) W_eventHttpRetCode(ctx context.Context, module common.Module, eventId uint32, code uint32) errno.Error {
	w, err := f.getEventWriter(eventId)
	if err != 0 {
		return err
	}

	w.WriteHeader(int(code))

	return 0
}
