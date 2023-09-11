package auth

import (
	"github.com/taubyte/go-interfaces/common"
	commonSpecs "github.com/taubyte/go-specs/common"
	"github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/libdream"
)

func init() {
	if err := libdream.Registry.Set(commonSpecs.Auth, nil, createAuthClient); err != nil {
		panic(err)
	}

}

func createAuthClient(node peer.Node, config *common.ClientConfig) (common.Client, error) {
	return New(node.Context(), node)
}
