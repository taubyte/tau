package gateway

import (
	http "github.com/taubyte/http"
	"github.com/taubyte/tau/p2p/peer"
)

func (g *Gateway) Node() peer.Node {
	return g.node
}

func (g *Gateway) Http() http.Service {
	return g.http
}

func (g *Gateway) Close() error {
	return g.substrateClient.Close()
}
