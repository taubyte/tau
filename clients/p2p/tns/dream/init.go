package dream

import (
	"github.com/taubyte/tau/clients/p2p/tns"
	"github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	commonSpecs "github.com/taubyte/tau/legacy/specs/common"
	"github.com/taubyte/tau/p2p/peer"
)

func init() {
	if err := dream.Registry.Set(commonSpecs.TNS, nil, createTNSClient); err != nil {
		panic(err)
	}
}

func createTNSClient(node peer.Node, config *common.ClientConfig) (common.Client, error) {
	return tns.New(node.Context(), node)
}
