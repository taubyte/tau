package gateway

import (
	"context"

	substrate "github.com/taubyte/go-interfaces/services/substrate/components/http"
	http "github.com/taubyte/http"
	"github.com/taubyte/p2p/peer"
)

type Gateway struct {
	ctx  context.Context
	node peer.Node
	http http.Service

	substrateClient substrate.Client

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
