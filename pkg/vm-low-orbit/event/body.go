package event

import (
	"context"
	"io"
	"net/http"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) readHttpEventBody(ctx context.Context, module common.Module, eventId uint32, bufPtr uint32, bufSize uint32, countPtr uint32) uint32 {
	r, err := f.getEventRequest(eventId)
	if err != 0 {
		return uint32(err)
	}

	buf := make([]byte, bufSize)

	n, err0 := r.Body.Read(buf)
	if err0 != nil && err0 != io.EOF {
		return uint32(errno.ErrorHttpReadBody)
	}

	err = f.WriteUint32Le(module, countPtr, uint32(n))
	if err != 0 {
		return uint32(err)
	}

	err = f.WriteBytes(module, bufPtr, buf)
	if err != 0 {
		return uint32(err)
	}

	if err0 == io.EOF {
		return uint32(errno.ErrorEOF)
	}

	return 0
}

func (f *Factory) closeHttpEventBody(ctx context.Context, module common.Module, eventId uint32) uint32 {
	r, err := f.getEventRequest(eventId)
	if err != 0 {
		return uint32(err)
	}

	err0 := r.Body.Close()
	if err0 != nil {
		return uint32(errno.ErrorCloseBody)
	}

	return 0
}

func (f *Factory) eventHttpWrite(ctx context.Context, module common.Module, eventId, bufPtr, bufSize, wroteN uint32) uint32 {
	w, err := f.getEventWriter(eventId)
	if err != 0 {
		return uint32(err)
	}

	buf, err := f.ReadBytes(module, bufPtr, bufSize)
	if err != 0 {
		return uint32(err)
	}

	n, err0 := w.Write(buf)
	if err0 != nil {
		f.WriteUint32Le(module, wroteN, uint32(n))
		return uint32(errno.ErrorHttpWrite)
	}

	return uint32(f.WriteUint32Le(module, wroteN, uint32(n)))
}

func (f *Factory) eventHttpFlush(ctx context.Context, module common.Module, eventId uint32) uint32 {
	w, err := f.getEventWriter(eventId)
	if err != 0 {
		return uint32(err)
	}

	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
		return 0
	}

	return uint32(errno.ErrorHttpWrite)
}
