package auth

import (
	"github.com/taubyte/go-interfaces/common"
	commonSpecs "github.com/taubyte/go-specs/common"
	"github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/libdream"
)

func init() {
	libdream.Registry.Set(commonSpecs.Auth, nil,
		func(n peer.Node, cc *common.ClientConfig) (common.Client, error) {
			return New(n.Context(), n)
		})
}
