package gateway

import (
	"github.com/taubyte/tau/p2p/peer"
	http "github.com/taubyte/tau/pkg/http"
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
