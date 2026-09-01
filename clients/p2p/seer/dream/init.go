package dream

import (
	"github.com/taubyte/tau/clients/p2p/seer"
	"github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	commonSpecs "github.com/taubyte/tau/legacy/specs/common"
	"github.com/taubyte/tau/p2p/peer"
)

func init() {
	if err := dream.Registry.Set(commonSpecs.Seer, nil, createSeerClient); err != nil {
		panic(err)
	}
}

func createSeerClient(node peer.Node, config *common.ClientConfig) (common.Client, error) {
	return seer.New(node.Context(), node)
}
