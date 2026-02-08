//go:build !web3
// +build !web3

package substrate

import (
	"github.com/taubyte/tau/pkg/config"
)

func (srv *Service) attachNodeIpfs(_ config.Config) (err error) {
	return nil
}
