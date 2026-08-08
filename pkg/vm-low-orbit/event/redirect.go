package event

import (
	"context"
	"net/http"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) eventHttpRedirect(ctx context.Context, module common.Module, eventId uint32, urlPtr uint32, urlLen uint32, code uint32) uint32 {
	// http.Redirect calls w.WriteHeader(code), which panics for a code < 100 or
	// > 999 on the gateway's forwarding goroutine (see eventHttpRetCode). Reject
	// an out-of-range code before touching the writer.
	if code < 100 || code > 999 {
		return uint32(errno.ErrorHttpWrite)
	}

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
