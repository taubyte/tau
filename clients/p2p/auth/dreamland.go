package p2p

import (
	dreamlandRegistry "github.com/taubyte/dreamland/core/registry"
	"github.com/taubyte/go-interfaces/common"
	p2p "github.com/taubyte/go-interfaces/p2p/peer"
)

func init() {
	dreamlandRegistry.Registry.Auth.Client = createAuthClient
}

func createAuthClient(node p2p.Node, config *common.ClientConfig) (common.Client, error) {
	return New(node.Context(), node)
}
