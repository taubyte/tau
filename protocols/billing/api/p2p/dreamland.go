package p2p

import (
	dreamlandRegistry "bitbucket.org/taubyte/dreamland/registry"
	"github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/go-interfaces/p2p/peer"
)

func init() {
	dreamlandRegistry.Registry.Billing.Client = createBillingClient
}

func createBillingClient(node peer.Node, config *common.ClientConfig) (common.Client, error) {
	return New(node.Context(), node)
}
