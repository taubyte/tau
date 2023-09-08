package monkey

import (
	"github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/libdream"
)

func init() {
	libdream.Registry.Monkey.Client = createMonkeyClient
}

func createMonkeyClient(node peer.Node, config *common.ClientConfig) (common.Client, error) {
	return New(node.Context(), node)
}
