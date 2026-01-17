package dream

import (
	"github.com/taubyte/tau/clients/p2p/patrick"
	"github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/p2p/peer"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
)

func init() {
	if err := dream.Registry.Set(commonSpecs.Patrick, nil, createPatrickClient); err != nil {
		panic(err)
	}

}

func createPatrickClient(node peer.Node, config *common.ClientConfig) (common.Client, error) {
	return patrick.New(node.Context(), node)
}
