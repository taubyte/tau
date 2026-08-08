package event

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) eventHttpRetCode(ctx context.Context, module common.Module, eventId uint32, code uint32) uint32 {
	// net/http's WriteHeader panics for a status code < 100 or > 999. For a
	// gateway-fronted function that panic runs on the response-forwarding
	// goroutine with no recover, so an out-of-range guest code would crash the
	// shared gateway. Reject it here instead.
	if code < 100 || code > 999 {
		return uint32(errno.ErrorHttpWrite)
	}

	w, err := f.getEventWriter(eventId)
	if err != 0 {
		return uint32(err)
	}

	w.WriteHeader(int(code))

	return 0
}
