package services

import (
	"fmt"

	commonIface "github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/go-interfaces/services/substrate"
	peer "github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/libdream/registry"
)

func (u *Universe) CreateSubstrateService(config *commonIface.ServiceConfig) (peer.Node, error) {
	var err error

	if registry.Registry.Substrate.Service == nil {
		return nil, fmt.Errorf(`service is nil, have you imported _ "github.com/taubyte/tau/protocols/substrate"`)
	}

	substrateNode, err := registry.Registry.Substrate.Service(u.ctx, config)
	if err != nil {
		return nil, err
	}

	_substrate, ok := substrateNode.(substrate.Service)
	if !ok {
		return nil, fmt.Errorf("failed type casting node into a service")
	}

	u.registerService("substrate", _substrate)
	u.toClose(_substrate)

	return _substrate.Node(), nil
}
