package dream

import (
	"github.com/taubyte/tau/clients/p2p/hoarder"
	"github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	commonSpecs "github.com/taubyte/tau/legacy/specs/common"
	"github.com/taubyte/tau/p2p/peer"
)

func init() {
	if err := dream.Registry.Set(commonSpecs.Hoarder, nil, createHoarderClient); err != nil {
		panic(err)
	}
}

func createHoarderClient(node peer.Node, config *common.ClientConfig) (common.Client, error) {
	return hoarder.New(node.Context(), node)
}
