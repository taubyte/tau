package libdream

import (
	"fmt"

	commonIface "github.com/taubyte/go-interfaces/common"
	authIface "github.com/taubyte/go-interfaces/services/auth"
	peer "github.com/taubyte/p2p/peer"
)

func (u *Universe) CreateAuthService(config *commonIface.ServiceConfig) (peer.Node, error) {
	var err error

	if Registry.Auth.Service == nil {
		return nil, fmt.Errorf("service is nil, have you imported _ \"github.com/taubyte/tau/protocols/auth\"")
	}

	auth, err := Registry.Auth.Service(u.ctx, config)
	if err != nil {
		return nil, err
	}

	_auth, ok := auth.(authIface.Service)
	if !ok {
		return nil, fmt.Errorf("failed type casting auth into a service")
	}

	u.registerService("auth", _auth)
	u.toClose(_auth)

	return _auth.Node(), nil
}

func (s *Simple) CreateAuthClient(config *commonIface.ClientConfig) error {
	if Registry.Auth.Client == nil {
		return fmt.Errorf("client is nil, have you imported _ \"github.com/taubyte/tau/clients/p2p/auth\"")
	}

	_auth, err := Registry.Auth.Client(s.Node, config)
	if err != nil {
		return err
	}

	var ok bool
	s.Clients.auth, ok = _auth.(authIface.Client)
	if !ok {
		return fmt.Errorf("setting auth client failed, not OK")
	}

	return nil

}
