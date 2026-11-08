package gateway

import (
	"context"

	"github.com/taubyte/tau/core/services/substrate"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/streams/client"
	tauConfig "github.com/taubyte/tau/pkg/config"
	http "github.com/taubyte/tau/pkg/http"
	"github.com/taubyte/tau/services/substrate/components/metrics"
)

type Gateway struct {
	ctx        context.Context
	node       peer.Node
	clientNode peer.Node // the node substrateClient runs on
	http       http.Service

	substrateClient substrate.ProxyClient
	local           tauConfig.LocalHTTP // the substrate in this process, if any

	cluster string
	dev     bool
	verbose bool
}

type wrappedResponse struct {
	metrics metrics.Iface
	*client.Response
}
