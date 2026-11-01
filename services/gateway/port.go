package gateway

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
)

// A workload declaring `ports: {http: 8080, metrics: 9090}` is invoked on the first.
// A route naming `port: metrics` is dialled instead: the worker hands over a
// connection to that port (Workers.Conn) and the request is spoken over it as HTTP.
// Placement still chooses the node.

// deliver is one request to where the route sends it: invoked, or spoken over
// a connection to the port the route named.
func (s *Service) deliver(ctx context.Context, at specs.Address, port string, req *invoke.Request) (*invoke.Response, error) {
	if port == "" {
		return s.workers.Invoke(ctx, at, req)
	}

	// One connection per request: a pooled connection would pin later requests to
	// whichever node took the first.
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return s.workers.Conn(ctx, at, port)
		},
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()

	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	path := req.Path
	if path == "" {
		path = "/"
	}

	// The dial ignores this host; the caller's Host header is carried below.
	out, err := http.NewRequestWithContext(ctx, method, "http://workload.invalid"+path, bytes.NewReader(req.Body))
	if err != nil {
		return nil, fmt.Errorf("gateway: building the request for port %q: %w", port, err)
	}
	for k, vs := range req.Headers {
		for _, v := range vs {
			out.Header.Add(k, v)
		}
	}

	res, err := transport.RoundTrip(out)
	if err != nil {
		return nil, fmt.Errorf("gateway: %s/%s on port %q: %w", at.Kind, at.Name, port, err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("gateway: reading %s/%s on port %q: %w", at.Kind, at.Name, port, err)
	}
	return &invoke.Response{Status: res.StatusCode, Headers: res.Header, Body: body}, nil
}
