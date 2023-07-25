package patrick

import (
	dreamlandRegistry "github.com/taubyte/dreamland/core/registry"
	"github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/p2p/peer"
)

func init() {
	dreamlandRegistry.Registry.Patrick.Client = createPatrickClient
}

func createPatrickClient(node peer.Node, config *common.ClientConfig) (common.Client, error) {
	return New(node.Context(), node)
}
