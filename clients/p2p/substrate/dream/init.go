package dream

import (
	"github.com/taubyte/tau/clients/p2p/substrate"
	"github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	commonSpecs "github.com/taubyte/tau/legacy/specs/common"
	"github.com/taubyte/tau/p2p/peer"
)

func init() {
	if err := dream.Registry.Set(commonSpecs.Substrate, nil, createSubstrateClient); err != nil {
		panic(err)
	}
}

func createSubstrateClient(node peer.Node, config *common.ClientConfig) (common.Client, error) {
	return substrate.New(node.Context(), node)
}
