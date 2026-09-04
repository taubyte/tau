package wasm

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/samyfodil/wazy/imports/wasip2"

	"github.com/taubyte/tau/pkg/invoke"
)

// http.go drives a component that exports the standard world.
//
// # It is an adapter and nothing more
//
// wazy synthesizes the guest's WASI types from a net/http request and writes
// back what the guest produced, so what is left here is turning invoke's own
// Request and Response into and out of net/http's. That is the whole of what
// makes a component built for wasmtime serve, Spin or wasmCloud run here.
//
// # A non-HTTP invocation still reaches it
//
// A pubsub delivery has no method and no path, and this gives it POST and "/"
// — because the standard world has no way to express an invocation that is not
// an HTTP exchange, and refusing to deliver one at all would be worse than
// delivering it as the closest thing the world can say. A component whose
// author wants the difference exports the native world instead, which carries
// it exactly.

// defaultMethod and defaultPath are what a non-HTTP invocation arrives as.
const (
	defaultMethod = http.MethodPost
	defaultPath   = "/"
)

func (f *function) serveHTTP(ctx context.Context, req *invoke.Request) (*invoke.Response, error) {
	if req == nil {
		req = &invoke.Request{}
	}

	method := req.Method
	if method == "" {
		method = defaultMethod
	}
	path := req.Path
	if path == "" {
		path = defaultPath
	}

	var body io.Reader = http.NoBody
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}

	// THE HOST IS A PLACEHOLDER AND NOT THE CALLER'S. What reached this node
	// was routed by a gateway that already matched the name; handing the guest
	// a host it might act on would be handing it a routing decision that was
	// made somewhere it cannot see.
	inbound := httptest.NewRequestWithContext(ctx, method, "http://workload.invalid"+path, body)
	for name, values := range req.Headers {
		for _, value := range values {
			inbound.Header.Add(name, value)
		}
	}

	recorder := httptest.NewRecorder()
	wasip2.Handler(f.instance).ServeHTTP(recorder, inbound)

	result := recorder.Result()
	defer result.Body.Close()

	answered, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, err
	}

	headers := map[string][]string(result.Header)
	if len(headers) == 0 {
		headers = nil
	}

	return &invoke.Response{Status: result.StatusCode, Headers: headers, Body: answered}, nil
}
