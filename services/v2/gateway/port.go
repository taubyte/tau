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

// port.go delivers a request to the port a route NAMED.
//
// # Invoke answers on one port, and a workload may listen on several
//
// A workload that declares `ports: {http: 8080, metrics: 9090}` is invoked on
// the first of them by name — that is the container tier's one request path.
// A route that says `handler: {application: {name: api, port: metrics}}` is
// asking for the other one, and invoking would have answered it from the
// wrong socket with nothing to say so.
//
// So a named port is DIALLED rather than invoked: the worker hands over a
// connection to that port (Workers.Conn — the same seam a proxy and a channel
// take), and the request is spoken over it as HTTP. Placement still chooses
// the node; this only chooses the socket on it.

// deliver is one request to where the route sends it: invoked, or spoken over
// a connection to the port the route named.
func (s *Service) deliver(ctx context.Context, at specs.Address, port string, req *invoke.Request) (*invoke.Response, error) {
	if port == "" {
		return s.workers.Invoke(ctx, at, req)
	}

	// ONE CONNECTION PER REQUEST, for the reason node/model gives: placement
	// decides which node answers, and a pooled connection would pin every
	// later request to whichever node took the first.
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

	// THE HOST IS A PLACEHOLDER the dial ignores; what the workload is told
	// it was asked for is the Host header the caller sent, carried below.
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
