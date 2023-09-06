package gateway

import (
	"context"

	"github.com/taubyte/go-interfaces/services/tns"
	http "github.com/taubyte/http"
	"github.com/taubyte/p2p/peer"
	"github.com/taubyte/p2p/streams/client"
)

var (
	SubstrateThresholdRatio = 3
)

type Gateway struct {
	ctx  context.Context
	node peer.Node
	tns  tns.Client
	http http.Service
	// matchTimeout time.Duration

	p2pClient *client.Client

	substrateCount int

	dev     bool
	verbose bool
}

func (g *Gateway) Context() context.Context {
	return g.ctx
}

func (g *Gateway) Node() peer.Node {
	return g.node
}

func (g *Gateway) Http() http.Service {
	return g.http
}

func (g *Gateway) Tns() tns.Client {
	return g.tns
}

func (g *Gateway) Dev() bool {
	return g.dev
}
