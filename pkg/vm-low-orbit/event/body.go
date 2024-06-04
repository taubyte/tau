package event

import (
	"context"
	"io"
	"net/http"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) W_readHttpEventBody(ctx context.Context, module common.Module, eventId uint32, bufPtr uint32, bufSize uint32, countPtr uint32) (err errno.Error) {
	r, err := f.getEventRequest(eventId)
	if err != 0 {
		return err
	}

	buf := make([]byte, bufSize)

	n, err0 := r.Body.Read(buf)
	if err0 != nil && err0 != io.EOF {
		return errno.ErrorHttpReadBody
	}

	err = f.WriteUint32Le(module, countPtr, uint32(n))
	if err != 0 {
		return
	}

	err = f.WriteBytes(module, bufPtr, buf)
	if err != 0 {
		return
	}

	if err0 == io.EOF {
		return errno.ErrorEOF
	}

	return 0
}

func (f *Factory) W_closeHttpEventBody(ctx context.Context, module common.Module, eventId uint32) errno.Error {
	r, err := f.getEventRequest(eventId)
	if err != 0 {
		return err
	}

	err0 := r.Body.Close()
	if err0 != nil {
		return errno.ErrorCloseBody
	}

	return 0
}

func (f *Factory) W_eventHttpWrite(ctx context.Context, module common.Module, eventId, bufPtr, bufSize, wroteN uint32) (err errno.Error) {
	w, err := f.getEventWriter(eventId)
	if err != 0 {
		return
	}

	buf, err := f.ReadBytes(module, bufPtr, bufSize)
	if err != 0 {
		return
	}

	n, err0 := w.Write(buf)
	if err0 != nil {
		f.WriteUint32Le(module, wroteN, uint32(n))
		return errno.ErrorHttpWrite
	}

	return f.WriteUint32Le(module, wroteN, uint32(n))
}

func (f *Factory) W_eventHttpFlush(ctx context.Context, module common.Module, eventId uint32) (err errno.Error) {
	w, err := f.getEventWriter(eventId)
	if err != 0 {
		return
	}

	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
		return 0
	}

	return errno.ErrorHttpWrite
}
