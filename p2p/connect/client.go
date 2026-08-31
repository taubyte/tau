package connect

import (
	"context"
	"errors"
	"net"
	"net/http"

	gostream "github.com/libp2p/go-libp2p-gostream"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// Client is the pair a generated connect client is built from: an
// *http.Client that reaches exactly one peer on exactly one protocol, and the
// base URL to address it by.
//
//	c, err := connect.NewClient(h, pid, "/tau/seer/v2")
//	seer := seerv2connect.NewSeerServiceClient(c.HTTP(), c.URL())
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
			return gostream.Dial(ctx, h, p, id)
		},
		Protocols: h2(),
	}

	return &Client{
		transport: t,
		// No Timeout: it would cap a stream's whole lifetime, and a
		// server-streaming call is meant to outlive any such number. Callers
		// bound a call with its context.
		http: &http.Client{Transport: t},
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
