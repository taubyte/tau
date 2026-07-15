package event

import (
	"context"

	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) eventHttpRetCode(ctx context.Context, module common.Module, eventId uint32, code uint32) uint32 {
	w, err := f.getEventWriter(eventId)
	if err != 0 {
		return uint32(err)
	}

	w.WriteHeader(int(code))

	return 0
}
