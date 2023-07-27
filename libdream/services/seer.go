package services

import (
	"fmt"

	commonIface "github.com/taubyte/go-interfaces/common"
	seerIface "github.com/taubyte/go-interfaces/services/seer"
	peer "github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/libdream/registry"
)

func (u *Universe) CreateSeerService(config *commonIface.ServiceConfig) (peer.Node, error) {
	if registry.Registry.Seer.Service == nil {
		return nil, fmt.Errorf(`Service is nil, have you imported _ "github.com/taubyte/tau/protocols/seer"`)
	}

	seer, err := registry.Registry.Seer.Service(u.ctx, config)
	if err != nil {
		return nil, err
	}

	_seer, ok := seer.(seerIface.Service)
	if !ok {
		return nil, fmt.Errorf("failed type casting seer into a service")
	}

	u.registerService("seer", _seer)
	u.toClose(_seer)

	return _seer.Node(), nil
}

func (s *Simple) CreateSeerClient(config *commonIface.ClientConfig) error {
	if registry.Registry.Seer.Client == nil {
		return fmt.Errorf(`client is nil, have you imported _ "github.com/taubyte/tau/clients/p2p/seer"`)
	}

	_seer, err := registry.Registry.Seer.Client(s.Node, config)
	if err != nil {
		return err
	}

	var ok bool
	s.Clients.seer, ok = _seer.(seerIface.Client)
	if !ok {
		return fmt.Errorf("setting seer client failed, not OK")
	}

	return nil

}
