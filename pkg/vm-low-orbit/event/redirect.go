package event

import (
	"context"
	"net/http"

	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) eventHttpRedirect(ctx context.Context, module common.Module, eventId uint32, urlPtr uint32, urlLen uint32, code uint32) uint32 {
	url, err := f.ReadString(module, urlPtr, urlLen)
	if err != 0 {
		return uint32(err)
	}

	w, err := f.getEventWriter(eventId)
	if err != 0 {
		return uint32(err)
	}

	r, err := f.getEventRequest(eventId)
	if err != 0 {
		return uint32(err)
	}

	http.Redirect(w, r, url, int(code))
	return 0
}
