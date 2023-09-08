package libdream

import (
	"fmt"

	commonIface "github.com/taubyte/go-interfaces/common"
	hoarderIface "github.com/taubyte/go-interfaces/services/hoarder"
	peer "github.com/taubyte/p2p/peer"
)

func (u *Universe) CreateHoarderService(config *commonIface.ServiceConfig) (peer.Node, error) {
	var err error

	if Registry.Hoarder.Service == nil {
		return nil, fmt.Errorf(`service is nil, have you imported _ "github.com/taubyte/tau/protocols/hoarder"`)
	}

	hoarder, err := Registry.Hoarder.Service(u.ctx, config)
	if err != nil {
		return nil, err
	}

	_hoarder, ok := hoarder.(hoarderIface.Service)
	if !ok {
		return nil, fmt.Errorf("failed type casting hoarder into a service")
	}

	u.registerService("hoarder", _hoarder)
	u.toClose(_hoarder)

	return _hoarder.Node(), nil
}

func (s *Simple) CreateHoarderClient(config *commonIface.ClientConfig) error {
	if Registry.Hoarder.Client == nil {
		return fmt.Errorf(`client is nil, have you imported _ "github.com/taubyte/tau/clients/p2p/hoarder"`)
	}

	_hoarder, err := Registry.Hoarder.Client(s.Node, config)
	if err != nil {
		return err
	}

	var ok bool
	s.Clients.hoarder, ok = _hoarder.(hoarderIface.Client)
	if !ok {
		return fmt.Errorf("setting hoarder client failed, not OK")
	}

	return nil

}
