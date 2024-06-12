package dream

import (
	"fmt"

	commonIface "github.com/taubyte/tau/core/common"
	peer "github.com/taubyte/tau/p2p/peer"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
	"golang.org/x/exp/slices"
)

func (u *Universe) defaultClients() map[string]*commonIface.ClientConfig {
	clients := make(map[string]*commonIface.ClientConfig)
	for _, name := range commonSpecs.P2PStreamServices {
		clients[name] = &commonIface.ClientConfig{}
	}

	return clients
}

func (u *Universe) getServiceByNameId(name, id string) (node commonIface.Service, exist bool) {
	u.lock.RLock()
	defer u.lock.RUnlock()
	serviceInfo, exist := u.service[name]
	if !exist {
		return
	}
	if serviceInfo == nil || !exist {
		return
	}

	node, exist = serviceInfo.nodes[id]
	return
}

func (u *Universe) getSimpleByNameId(name, id string) (simple *Simple, exist bool) {
	u.lock.RLock()
	defer u.lock.RUnlock()
	simple, exist = u.simples[name]
	if !exist {
		return
	}
	if simple == nil || !exist {
		return
	}

	return
}

func (u *Universe) killServiceByNameId(name, id string) error {
	node, exist := u.getServiceByNameId(name, id)
	if !exist {
		return fmt.Errorf("killing %s: %s failed with: does not exist", name, id)
	}

	serviceInfo, exist := u.service[name]
	if !exist {
		return fmt.Errorf("killing %s: %s failed with: does not exist", name, id)
	}

	u.lock.Lock()
	defer u.lock.Unlock()
	node.Close()
	delete(serviceInfo.nodes, id)
	delete(u.lookups, id)
	u.discardNode(node.Node(), false)

	node.Node().Close()

	return nil
}

func (u *Universe) killSimpleByNameId(name, id string) error {
	simple, exist := u.getSimpleByNameId(name, id)
	if !exist {
		return fmt.Errorf("killing %s: %s failed with: does not exist", name, id)
	}

	u.lock.Lock()
	defer u.lock.Unlock()
	simple.Close()
	delete(u.simples, name)
	delete(u.lookups, id)
	u.discardNode(simple.PeerNode(), false)

	return nil
}

func (u *Universe) KillNodeByNameID(name, id string) error {
	if slices.Contains(commonSpecs.Services, name) {
		return u.killServiceByNameId(name, id)
	} else {
		return u.killSimpleByNameId(name, id)
	}
}

func (u *Universe) startService(protocol string, config *commonIface.ServiceConfig) (peer.Node, error) {
	creationMethod, err := Registry.service(protocol)
	if err != nil {
		return nil, err
	}

	service, err := creationMethod(u, config)
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

	client, err := creationMethod(s.Node, config)
	if err != nil {
		return err
	}

	s.lock.Lock()
	s.clients[name] = client
	s.lock.Unlock()

	if _, err := s.getClient(name); err != nil {
		return err
	}

	return nil
}

func (u *Universe) discardNode(node peer.Node, lock bool) {
	// ref: https://stackoverflow.com/questions/20545743/how-to-remove-items-from-a-slice-while-ranging-over-it
	if lock {
		u.lock.Lock()
		defer u.lock.Unlock()
	}

	for i := len(u.all) - 1; i >= 0; i-- {
		if u.all[i] == node {
			u.all = append(u.all[:i], u.all[i+1:]...)
		}
	}
}
