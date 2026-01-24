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

// HTTP -> Stream
func Frontend(w http.ResponseWriter, r *http.Request, stream io.ReadWriter) error {
	ctx, ctxC := context.WithCancel(context.Background())
	defer ctxC()

	done := make(chan error)
	go func() {
		var exitError error

		defer func() {
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
					err = headersOp(w, payload)
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

	_, _, err := requestToStream(stream, r)
	if err != nil {
		return fmt.Errorf("request stream failed with %w", err)
	}

	return <-done
}

func requestToStream(stream io.Writer, r *http.Request) (int64, int64, error) {
	pack := packer.New(Magic, Version)

	rpayload := requestToPayload(r)

	rpbuf, err := rpayload.Encode()
	if err != nil {
		return 0, 0, fmt.Errorf("encoding request payload failed: %w", err)
	}

	hdrlen := int64(len(rpbuf))
	err = pack.Send(RequestOp, stream, bytes.NewBuffer(rpbuf), hdrlen)
	if err != nil {
		return 0, 0, fmt.Errorf("sending request payload failed: %w", err)
	}

	bodylen, _ := pack.Stream(BodyOp, stream, r.Body, BodyStreamBufferSize)
	r.Body.Close()

	return hdrlen, bodylen, nil
}

func headersOp(w http.ResponseWriter, r io.Reader) error {
	dec := cbor.NewDecoder(r)
	var obj headersOpPayload
	err := dec.Decode(&obj)
	if err != nil {
		return fmt.Errorf("decoding headers operation payload failed: %w", err)
	}

	// delete
	for k := range w.Header() {
		if _, ok := obj.Headers[k]; !ok {
			w.Header().Del(k)
		}
	}

	// add/set
	for k, v := range obj.Headers {
		for i := 0; i < len(v); i++ {
			if i == 0 {
				w.Header().Set(k, v[i])
			} else {
				w.Header().Add(k, v[i])
			}
		}
	}

	w.WriteHeader(int(obj.Code))

	return nil
}

func bodyOp(w http.ResponseWriter, r io.Reader) (int64, error) {
	return io.Copy(w, r)
}
