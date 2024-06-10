package hoarder

import (
	"github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/p2p/peer"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
)

func init() {
	if err := dream.Registry.Set(commonSpecs.Hoarder, nil, createHoarderClient); err != nil {
		panic(err)
	}
}

func createHoarderClient(node peer.Node, config *common.ClientConfig) (common.Client, error) {
	return New(node.Context(), node)
}
