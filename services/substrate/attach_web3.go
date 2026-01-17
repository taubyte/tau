//go:build web3
// +build web3

package substrate

import (
	"fmt"

	"github.com/taubyte/tau/config"
	ipfs "github.com/taubyte/tau/services/substrate/components/ipfs"
)

func (srv *Service) attachNodeIpfs(config *config.Node) (err error) {
	ipfsPort, ok := config.Ports["ipfs"]
	if !ok {
		err = fmt.Errorf("did not find ipfs port in config")
		return

	}

	srv.components.ipfs, err = ipfs.New(srv.node.Context(), ipfs.Public(), ipfs.Listen([]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", ipfsPort)}))
	return
}
