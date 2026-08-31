package dream

import (
	"github.com/taubyte/tau/clients/p2p/patrick"
	"github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	commonSpecs "github.com/taubyte/tau/legacy/specs/common"
	"github.com/taubyte/tau/p2p/peer"
)

func init() {
	if err := dream.Registry.Set(commonSpecs.Patrick, nil, createPatrickClient); err != nil {
		panic(err)
	}

}

func createPatrickClient(node peer.Node, config *common.ClientConfig) (common.Client, error) {
	return patrick.New(node.Context(), node)
}
