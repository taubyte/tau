package patrick

import (
	"github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
)

func init() {
	if err := dream.Registry.Set(commonSpecs.Patrick, nil, createPatrickClient); err != nil {
		panic(err)
	}

}

func createPatrickClient(node peer.Node, config *common.ClientConfig) (common.Client, error) {
	return New(node.Context(), node)
}
