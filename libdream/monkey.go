package libdream

import (
	"fmt"

	commonIface "github.com/taubyte/go-interfaces/common"
	monkeyIface "github.com/taubyte/go-interfaces/services/monkey"
	peer "github.com/taubyte/p2p/peer"
)

func (u *Universe) CreateMonkeyService(config *commonIface.ServiceConfig) (peer.Node, error) {
	if Registry.Monkey.Service == nil {
		return nil, fmt.Errorf(`service is nil, have you imported _ "github.com/taubyte/tau/protocols/monkey"`)
	}

	monkey, err := Registry.Monkey.Service(u.ctx, config)
	if err != nil {
		return nil, err
	}

	_monkey, ok := monkey.(monkeyIface.Service)
	if !ok {
		return nil, fmt.Errorf("failed type casting monkey into a service")
	}

	u.registerService("monkey", _monkey)
	u.toClose(_monkey)

	return _monkey.Node(), nil
}

func (s *Simple) CreateMonkeyClient(config *commonIface.ClientConfig) error {
	if Registry.Monkey.Client == nil {
		return fmt.Errorf(`client is nil, have you imported _ "github.com/taubyte/tau/clients/p2p/monkey"`)
	}

	_monkey, err := Registry.Monkey.Client(s.Node, config)
	if err != nil {
		return err
	}

	var ok bool
	s.Clients.monkey, ok = _monkey.(monkeyIface.Client)
	if !ok {
		return fmt.Errorf("setting monkey client failed, not OK")
	}

	return nil

}
