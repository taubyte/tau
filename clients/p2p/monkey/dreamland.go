package p2p

import (
	dreamlandRegistry "bitbucket.org/taubyte/dreamland/registry"
	"github.com/taubyte/go-interfaces/common"
	peer "github.com/taubyte/go-interfaces/p2p/peer"
)

func init() {
	dreamlandRegistry.Registry.Monkey.Client = createMonkeyClient
}

func createMonkeyClient(node peer.Node, config *common.ClientConfig) (common.Client, error) {
	return New(node.Context(), node)
}
