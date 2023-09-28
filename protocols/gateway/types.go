package gateway

import (
	"context"

	"github.com/taubyte/go-interfaces/services/substrate"
	http "github.com/taubyte/http"
	"github.com/taubyte/p2p/peer"
	"github.com/taubyte/p2p/streams/client"
	"github.com/taubyte/tau/protocols/substrate/components/metrics"
)

type Gateway struct {
	ctx  context.Context
	node peer.Node
	http http.Service

	substrateClient substrate.ProxyClient

	dev     bool
	verbose bool
}

type wrappedResponse struct {
	metrics metrics.Iface
	*client.Response
}
