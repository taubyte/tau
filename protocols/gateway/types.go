package gateway

import (
	"context"

	http "github.com/taubyte/http"
	"github.com/taubyte/p2p/peer"
	"github.com/taubyte/p2p/streams/client"
)

type Gateway struct {
	ctx  context.Context
	node peer.Node
	// tns  tns.Client
	http http.Service
	// matchTimeout time.Duration

	substrateClient *client.Client

	connectedSubstrate uint64

	dev     bool
	verbose bool
}

func (g *Gateway) Node() peer.Node {
	return g.node
}

func (g *Gateway) Http() http.Service {
	return g.http
}

func (g *Gateway) Close() error {
	return nil
}
