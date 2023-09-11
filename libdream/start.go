package libdream

import (
	commonIface "github.com/taubyte/go-interfaces/common"
	peer "github.com/taubyte/p2p/peer"
)

func (u *Universe) startService(protocol string, config *commonIface.ServiceConfig) (peer.Node, error) {
	creationMethod, err := Registry.service(protocol)
	if err != nil {
		return nil, err
	}

	service, err := creationMethod(u.ctx, config)
	if err != nil {
		return nil, err
	}

	u.registerService(protocol, service)
	u.toClose(service)

	return service.Node(), nil
}

func (s *Simple) startClient(name string, config *commonIface.ClientConfig) error {
	creationMethod, err := Registry.client(name)
	if err != nil {
		return err
	}

	if _, err := s.getClient(name); err != nil {
		return err
	}

	client, err := creationMethod(s.Node, config)
	if err != nil {
		return err
	}

	s.lock.Lock()
	s.clients[name] = client
	s.lock.Unlock()

	return nil
}
