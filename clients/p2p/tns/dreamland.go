package tns

import (
	"github.com/taubyte/go-interfaces/common"
	commonSpecs "github.com/taubyte/go-specs/common"
	"github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/libdream"
)

func init() {
	if err := libdream.Registry.Set(commonSpecs.TNS, nil, createTNSClient); err != nil {
		panic(err)
	}
}

func createTNSClient(node peer.Node, config *common.ClientConfig) (common.Client, error) {
	return New(node.Context(), node)
}
