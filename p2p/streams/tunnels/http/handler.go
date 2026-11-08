package httptun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/tau/p2p/streams/packer"
)

// Stream -> HTTP
func Backend(stream io.ReadWriter) (http.ResponseWriter, *http.Request, error) {
	pack := packer.New(Magic, Version)

	var rpbuf bytes.Buffer
	ch, _, err := pack.Recv(stream, &rpbuf)
	if err != nil {
		return nil, nil, fmt.Errorf("reading http request failed with %w", err)
	}

	if ch != RequestOp {
		return nil, nil, fmt.Errorf("expected request payload, got channel %d", ch)
	}

	var rpayload requestPayload

	err = rpayload.Decode(rpbuf.Bytes())
	if err != nil {
		return nil, nil, fmt.Errorf("decoding request payload failed: %w", err)
	}

	req, err := payloadToRequest(&rpayload, stream)
	if err != nil {
		return nil, nil, fmt.Errorf("converting payload to request failed: %w", err)
	}

	return newResponseWriter(stream), req, nil

}

// Replayable says the request can be sent twice: a body is consumed as it is forwarded.
func Replayable(r *http.Request) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.ContentLength == 0
}

// HTTP -> Stream
func Frontend(w http.ResponseWriter, r *http.Request, stream io.ReadWriter) error {
	ctx := r.Context()

	// The body is read while the response is written. Without this, HTTP/1 makes
	// writing the response headers wait on a body read blocked on a stalled client.
	http.NewResponseController(w).EnableFullDuplex()

	// Closing the stream is what wakes the reader and a sender stuck on a full
	// stream window, so nothing below outlives the request or a client that left.
	defer closeStream(stream)
	defer context.AfterFunc(ctx, func() { closeStream(stream) })()

	replayable := Replayable(r)

	// buffered: if sending the request fails, nobody reads done and the reader must not block forever
	done := make(chan error, 1)
	go func() {
		var exitError error

		defer func() {
			// This goroutine processes untrusted peer frames outside net/http's
			// per-request recover, so a panic here would take down the process.
			// Convert it to an error the caller can surface instead.
			if rec := recover(); rec != nil {
				exitError = fmt.Errorf("panic while forwarding http response: %v", rec)
			}
			done <- exitError
		}()

		pack := packer.New(Magic, Version)

		for {
			select {
			case <-ctx.Done():
				return
			default:
				ch, n, err := pack.Next(stream)
				if err != nil {
					if errors.Is(err, io.EOF) {
						err = nil
					} else {
						exitError = fmt.Errorf("reading stream failed with %w", err)
					}
					return
				}

				payload := io.LimitReader(stream, n)
				var m int64
				switch ch {
				case HeadersOp:
					err = headersOp(w, payload, replayable)
				case BodyOp:
					m, err = bodyOp(w, payload)
					if m != n {
						err = fmt.Errorf("failed to forward body: expected %d bytes, forwarded %d", n, m)
					}
				default:
					err = fmt.Errorf("failed to process http response op: unknown channel %d", ch)
				}
				if err != nil {
					exitError = fmt.Errorf("processing HTTP response operation failed: %w", err)
					return
				}
			}
		}
	}()

	if err := requestToStream(stream, r); err != nil {
		return fmt.Errorf("request stream failed with %w", err)
	}

	// Not awaited: a backend may answer without reading the body, and closing a
	// stream's read side does not tell the sender, so this can block until
	// closeStream. It only touches r.Body, which net/http keeps safe after the handler.
	go func() {
		packer.New(Magic, Version).Stream(BodyOp, stream, r.Body, BodyStreamBufferSize)
		r.Body.Close()
	}()

	err := <-done
	if ctx.Err() != nil {
		return nil // the client left: there is nobody to report to and nothing went wrong here
	}
	return err
}

// closeStream tears the stream down in both directions. *client.Response resets.
func closeStream(stream io.ReadWriter) {
	switch s := stream.(type) {
	case interface{ Close() }:
		s.Close()
	case io.Closer:
		s.Close()
	}
}

func requestToStream(stream io.Writer, r *http.Request) error {
	pack := packer.New(Magic, Version)

	rpayload := requestToPayload(r)

	rpbuf, err := rpayload.Encode()
	if err != nil {
		return fmt.Errorf("encoding request payload failed: %w", err)
	}

	if err = pack.Send(RequestOp, stream, bytes.NewBuffer(rpbuf), int64(len(rpbuf))); err != nil {
		return fmt.Errorf("sending request payload failed: %w", err)
	}

	return nil
}

func headersOp(w http.ResponseWriter, r io.Reader, replayable bool) error {
	dec := cbor.NewDecoder(r)
	var obj headersOpPayload
	err := dec.Decode(&obj)
	if err != nil {
		return fmt.Errorf("decoding headers operation payload failed: %w", err)
	}

	if obj.Refused && replayable {
		return ErrRefused
	}

	// merge: the gateway's own headers (X-Substrate-Peer, CORS Vary) stay;
	// a header the function sets overrides the gateway's.
	for k, v := range obj.Headers {
		for i := 0; i < len(v); i++ {
			if i == 0 {
				w.Header().Set(k, v[i])
			} else {
				w.Header().Add(k, v[i])
			}
		}
	}

	// obj.Code is attacker-controlled: a substrate forwarding a guest's return
	// code, or any peer speaking this protocol. net/http's WriteHeader panics
	// for a code < 100 or > 999, and this runs on the Frontend goroutine below
	// with no net/http recover to catch it, so clamp an out-of-range code to 500
	// rather than crash the process.
	code := int(obj.Code)
	if code < 100 || code > 999 {
		code = http.StatusInternalServerError
	}
	w.WriteHeader(code)

	return nil
}

func bodyOp(w http.ResponseWriter, r io.Reader) (int64, error) {
	n, err := io.Copy(w, r)
	// As httputil.ReverseProxy: a body of unknown length may be a stream, so it
	// reaches the client now. A declared length stays in net/http's buffer.
	if err == nil && w.Header().Get("Content-Length") == "" {
		http.NewResponseController(w).Flush() // an error only means w cannot flush
	}
	return n, err
}
