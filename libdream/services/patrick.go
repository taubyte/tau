package services

import (
	"fmt"

	commonIface "github.com/taubyte/go-interfaces/common"
	patrickIface "github.com/taubyte/go-interfaces/services/patrick"
	peer "github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/libdream/registry"
)

func (u *Universe) CreatePatrickService(config *commonIface.ServiceConfig) (peer.Node, error) {
	var err error

	if registry.Registry.Patrick.Service == nil {
		return nil, fmt.Errorf(`Service is nil, have you imported _ "github.com/taubyte/tau/protocols/patrick"`)
	}

	patrick, err := registry.Registry.Patrick.Service(u.ctx, config)
	if err != nil {
		return nil, err
	}

	_patrick, ok := patrick.(patrickIface.Service)
	if !ok {
		return nil, fmt.Errorf("failed type casting patrick into a service")
	}

	u.registerService("patrick", _patrick)
	u.toClose(_patrick)

	return _patrick.Node(), nil
}

func (s *Simple) CreatePatrickClient(config *commonIface.ClientConfig) error {
	if registry.Registry.Patrick.Client == nil {
		return fmt.Errorf(`client is nil, have you imported _ "github.com/taubyte/tau/clients/p2p/patrick"`)
	}

	_patrick, err := registry.Registry.Patrick.Client(s.Node, config)
	if err != nil {
		return err
	}

	var ok bool
	s.Clients.patrick, ok = _patrick.(patrickIface.Client)
	if !ok {
		return fmt.Errorf("setting patrick client failed, not OK")
	}

	return nil

}
