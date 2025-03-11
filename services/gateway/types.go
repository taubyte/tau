package gateway

import (
	"context"

	"github.com/taubyte/tau/core/services/substrate"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/streams/client"
	http "github.com/taubyte/tau/pkg/http"
	"github.com/taubyte/tau/services/substrate/components/metrics"
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
