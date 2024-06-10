package httptun

import (
	"bytes"
	"io"
	"net/http"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/tau/p2p/streams/packer"
)

type responseWriter struct {
	header     http.Header
	pack       packer.Packer
	stream     io.Writer
	statusCode int32
}

type headersOpPayload struct {
	Headers http.Header `cbor:"1,keyasint"`
	Code    int32       `cbor:"2,keyasint"`
}

var _ http.ResponseWriter = &responseWriter{}

func newResponseWriter(stream io.ReadWriter) http.ResponseWriter {
	return &responseWriter{
		stream: stream,
		pack:   packer.New(Magic, Version),
		header: make(http.Header),
	}
}

func (w *responseWriter) Header() http.Header {
	return w.header
}

func (w *responseWriter) Write(b []byte) (int, error) {
	err := w.pack.Send(BodyOp, w.stream, bytes.NewBuffer(b), int64(len(b)))
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

// must be called
func (w *responseWriter) WriteHeader(statusCode int) {
	if w.statusCode == 0 {
		w.statusCode = int32(statusCode)
	}

	var buf bytes.Buffer
	enc := cbor.NewEncoder(&buf)
	obj := headersOpPayload{
		Headers: w.header,
		Code:    w.statusCode,
	}

	// should never fail
	enc.Encode(&obj)

	err := w.pack.Send(HeadersOp, w.stream, &buf, int64(buf.Len()))
	if err != nil {
		// only fails if connection closed so we stop here
		return
	}
}
