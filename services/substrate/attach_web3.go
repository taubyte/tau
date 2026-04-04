//go:build web3
// +build web3

package substrate

import (
	"fmt"

	"github.com/taubyte/tau/pkg/config"
	ipfs "github.com/taubyte/tau/services/substrate/components/ipfs"
)

func (srv *Service) attachNodeIpfs(cfg config.Config) (err error) {
	ports := cfg.Ports()
	ipfsPort, ok := ports["ipfs"]
	if !ok {
		err = fmt.Errorf("did not find ipfs port in config")
		return

	}

	srv.components.ipfs, err = ipfs.New(srv.node.Context(), ipfs.Public(), ipfs.Listen([]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", ipfsPort)}))
	return
}
