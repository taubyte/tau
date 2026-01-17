package dream

import (
	"github.com/taubyte/tau/clients/p2p/monkey"
	"github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/p2p/peer"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
)

func init() {
	if err := dream.Registry.Set(commonSpecs.Monkey, nil, createMonkeyClient); err != nil {
		panic(err)
	}

}

func createMonkeyClient(node peer.Node, config *common.ClientConfig) (common.Client, error) {
	return monkey.New(node.Context(), node)
}
