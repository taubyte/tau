package rpc

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"

	gostream "github.com/libp2p/go-libp2p-gostream"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	msmux "github.com/multiformats/go-multistream"
)

// Client is the pair a generated connect client is built from: an
// *http.Client that reaches exactly one peer on exactly one protocol, and the
// base URL to address it by.
//
//	c, err := connect.NewClient(h, pid, "/tau/register/v2")
//	register := registerv2connect.NewRegisterServiceClient(c.HTTP(), c.URL())
//
// One Client holds one libp2p stream and multiplexes every call over it, the
// way HTTP/2 is meant to be used. Opening a second Client to the same peer
// opens a second stream.
type Client struct {
	transport *http.Transport
	http      *http.Client
	url       string
}

// NewClient prepares calls to p on protocol id. Nothing is dialled until the
// first call, and that call's context governs the dial.
func NewClient(h host.Host, p peer.ID, id protocol.ID) (*Client, error) {
	if h == nil {
		return nil, errors.New("no host to dial from")
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, errors.New("no protocol to dial")
	}

	t := &http.Transport{
		// The address net/http would dial is a peer id, not a host:port.
		// gostream takes it from the closure instead, so the address the
		// transport passes in is ignored, and a proxy has nothing to say
		// about it.
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			conn, err := gostream.Dial(ctx, h, p, id)
			switch {
			case err == nil:
				return &stream{Conn: conn}, nil
			case ctx.Err() == nil:
				return nil, unreached{err}
			}
			return nil, err
		},
		Protocols: h2(),
	}

	return &Client{
		transport: t,
		// No Timeout: it would cap a stream's whole lifetime, and a
		// server-streaming call is meant to outlive any such number. Callers
		// bound a call with its context.
		http: &http.Client{Transport: refusals{t}},
		// The peer id is the authority. It is not resolved or dialled — the
		// transport already knows where it is going — but it makes the
		// :authority header say who the call was addressed to.
		url: "http://" + p.String(),
	}, nil
}

// HTTP is the connect.HTTPClient a generated client takes.
func (c *Client) HTTP() *http.Client { return c.http }

// URL is the base URL a generated client takes.
func (c *Client) URL() string { return c.url }

// Close drops the stream. Calls after it open a new one.
func (c *Client) Close() { c.transport.CloseIdleConnections() }

// stream is a dialled stream that remembers whether its peer refused it. A peer
// listed as serving the protocol is handed the stream before it answers, so one
// that does not serve it refuses on the first read, after the dial.
type stream struct {
	net.Conn
	refused atomic.Bool
}

func (s *stream) Read(b []byte) (int, error) {
	n, err := s.Conn.Read(b)
	if errors.Is(err, msmux.ErrNotSupported[protocol.ID]{}) {
		s.refused.Store(true)
	}
	return n, err
}

// refusals takes a call carried on a stream its peer refused for one that never
// reached the service. HTTP/2 holds the stream by then, so the call fails on the
// refusal, or on a conn that closed before the call was sent.
type refusals struct{ *http.Transport }

func (t refusals) RoundTrip(r *http.Request) (*http.Response, error) {
	var on *stream
	trace := &httptrace.ClientTrace{GotConn: func(c httptrace.GotConnInfo) { on, _ = c.Conn.(*stream) }}
	res, err := t.Transport.RoundTrip(r.WithContext(httptrace.WithClientTrace(r.Context(), trace)))
	if err != nil && on != nil && on.refused.Load() {
		return nil, unreached{err}
	}
	return res, err
}
