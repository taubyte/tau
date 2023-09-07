package services

import (
	"fmt"

	commonIface "github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/go-interfaces/services/gateway"
	peer "github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/libdream/registry"
)

func (u *Universe) CreateGatewayService(config *commonIface.ServiceConfig) (peer.Node, error) {
	var err error

	if registry.Registry.Gateway.Service == nil {
		return nil, fmt.Errorf(`service is nil, have you imported _ "github.com/taubyte/tau/protocols/gateway"`)
	}

	gatewayNode, err := registry.Registry.Gateway.Service(u.ctx, config)
	if err != nil {
		return nil, err
	}

	_gateway, ok := gatewayNode.(gateway.Service)
	if !ok {
		return nil, fmt.Errorf("gateway service is not a gateway interface")
	}

	u.registerService("gateway", _gateway)
	u.toClose(_gateway)

	return _gateway.Node(), nil
}
