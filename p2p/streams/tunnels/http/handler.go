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
		return nil, nil, errors.New("expected request payload")
	}

	var rpayload requestPayload

	err = rpayload.Decode(rpbuf.Bytes())
	if err != nil {
		return nil, nil, err
	}

	req, err := payloadToRequest(&rpayload, stream)
	if err != nil {
		return nil, nil, err
	}

	return newResponseWriter(stream), req, nil

}

// Note: make sure you call
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
					if err == io.EOF {
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
						err = errors.New("failed to forward body")
					}
				default:
					err = errors.New("failed to process http response op")
				}
				if err != nil {
					exitError = err
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
		return 0, 0, err
	}

	hdrlen := int64(len(rpbuf))
	err = pack.Send(RequestOp, stream, bytes.NewBuffer(rpbuf), hdrlen)
	if err != nil {
		return 0, 0, err
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
		return err
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
