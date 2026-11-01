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

// http.go drives a component exporting the standard world, adapting invoke's
// Request and Response to and from net/http for wazy; that is what lets components
// built for wasmtime, Spin or wasmCloud run here. A non-HTTP invocation arrives as
// POST "/", the closest the standard world can say; the native world carries the
// difference exactly.

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

	// The host is a placeholder: the gateway already routed on the real name.
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
