//go:build !web3
// +build !web3

package substrate

import (
	"github.com/taubyte/tau/config"
)

func (srv *Service) attachNodeIpfs(_ *config.Node) (err error) {
	return nil
}
