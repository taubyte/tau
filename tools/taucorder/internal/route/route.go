// Package route carries one ConnectRPC call to a node that serves it.
//
// Transport is an http.RoundTripper. The path of the request names the
// service ("/tau.register.v2.RegisterService/Resolve"); the service's proto
// package names the libp2p protocol (rpc.Protocol); a Picker names the peers
// that speak it, best first; and p2p/rpc carries the request there over a
// libp2p stream. The request and response bytes are never looked at, so every
// protocol connect-go serves — connect, grpc, grpc-web, unary and streaming —
// passes through unchanged.
//
// The same Transport backs the local listener's reverse proxy and the
// bridge's own generated clients, so a CLI read and a console read take one
// path.
package route

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/tools/taucorder/internal/policy"
)

// ErrNoPeer is a service nobody visible serves.
var ErrNoPeer = errors.New("no reachable node serves this service")

// Picker names the peers serving a protocol, best first.
type Picker interface {
	Pick(ctx context.Context, id protocol.ID) ([]peer.ID, error)
	// Failed reports that a call to pid on id failed to connect, so the next
	// pick prefers another.
	Failed(id protocol.ID, pid peer.ID)
}

// Transport routes by service. It is safe for concurrent use.
type Transport struct {
	host host.Host
	pick Picker

	mu      sync.Mutex
	clients map[key]*rpc.Client
}

type key struct {
	peer  peer.ID
	proto protocol.ID
}

// New is a Transport dialling from h.
func New(h host.Host, p Picker) *Transport {
	return &Transport{host: h, pick: p, clients: map[key]*rpc.Client{}}
}

// Protocol is the libp2p protocol a request path is served on.
func Protocol(path string) (protocol.ID, error) {
	svc, _, err := policy.Parse(path)
	if err != nil {
		return "", err
	}
	return rpc.Protocol(svc), nil
}

// RoundTrip sends req to the best peer serving its service.
//
// A request is sent once. A connection failure is reported to the Picker so
// the next call goes elsewhere, but this call is not retried: its body may
// already be half on the wire, and a streaming body cannot be replayed.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	id, err := Protocol(req.URL.Path)
	if err != nil {
		closeBody(req)
		return nil, err
	}
	peers, err := t.pick.Pick(req.Context(), id)
	if err != nil {
		closeBody(req)
		return nil, err
	}
	if len(peers) == 0 {
		closeBody(req)
		return nil, fmt.Errorf("%s: %w", id, ErrNoPeer)
	}
	pid := peers[0]
	c, err := t.client(pid, id)
	if err != nil {
		closeBody(req)
		return nil, err
	}

	out := req.Clone(req.Context())
	out.URL.Scheme = "http"
	out.URL.Host = pid.String()
	out.Host = pid.String()
	out.RequestURI = ""
	resp, err := c.HTTP().Transport.RoundTrip(out)
	if err != nil {
		t.pick.Failed(id, pid)
		t.drop(pid, id)
		return nil, fmt.Errorf("%s at %s: %w", id, pid, err)
	}
	return resp, nil
}

func closeBody(req *http.Request) {
	if req.Body != nil {
		req.Body.Close()
	}
}

func (t *Transport) client(pid peer.ID, id protocol.ID) (*rpc.Client, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	k := key{pid, id}
	if c, ok := t.clients[k]; ok {
		return c, nil
	}
	c, err := rpc.NewClient(t.host, pid, id)
	if err != nil {
		return nil, err
	}
	t.clients[k] = c
	return c, nil
}

func (t *Transport) drop(pid peer.ID, id protocol.ID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	k := key{pid, id}
	if c, ok := t.clients[k]; ok {
		c.Close()
		delete(t.clients, k)
	}
}

// Close drops every held stream.
func (t *Transport) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k, c := range t.clients {
		c.Close()
		delete(t.clients, k)
	}
}

// Client is an *http.Client over this transport, for generated clients:
//
//	registerv2connect.NewRegisterServiceClient(t.Client(), route.BaseURL)
func (t *Transport) Client() *http.Client { return &http.Client{Transport: t} }

// BaseURL is the base URL a generated client over Client takes. Its host is
// never dialled; the path alone decides where a call goes.
const BaseURL = "http://tau"
