package dream

import (
	"github.com/taubyte/tau/clients/p2p/auth"
	"github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	commonSpecs "github.com/taubyte/tau/legacy/specs/common"
	"github.com/taubyte/tau/p2p/peer"
)

func init() {
	if err := dream.Registry.Set(commonSpecs.Auth, nil, createAuthClient); err != nil {
		panic(err)
	}

}

func createAuthClient(node peer.Node, config *common.ClientConfig) (common.Client, error) {
	return auth.New(node.Context(), node)
}
