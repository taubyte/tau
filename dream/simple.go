package dream

import (
	"errors"
	"fmt"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	authIface "github.com/taubyte/tau/core/services/auth"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	monkeyIface "github.com/taubyte/tau/core/services/monkey"
	patrickIface "github.com/taubyte/tau/core/services/patrick"
	seerIface "github.com/taubyte/tau/core/services/seer"
	tnsIface "github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/p2p/keypair"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"

	peer "github.com/taubyte/tau/p2p/peer"
)

func (s *Simple) getClient(name string) (commonIface.Client, error) {
	s.lock.RLock()
	client, ok := s.clients[name]
	s.lock.RUnlock()
	if !ok {
		return nil, fmt.Errorf("client for protocol `%s` does not exist", name)
	}

	return client, nil
}

func (s *Simple) PeerNode() peer.Node {
	return s.Node
}

func (s *Simple) Seer() (seerIface.Client, error) {
	client, err := s.getClient(commonSpecs.Seer)
	if err != nil {
		return nil, err
	}

	seerClient, ok := client.(seerIface.Client)
	if !ok {
		return nil, errors.New("client is not a seer client")
	}

	return seerClient, nil
}

func (s *Simple) Auth() (authIface.Client, error) {
	client, err := s.getClient(commonSpecs.Auth)
	if err != nil {
		return nil, err
	}

	authClient, ok := client.(authIface.Client)
	if !ok {
		return nil, errors.New("client is not an auth client")
	}

	return authClient, nil
}

func (s *Simple) Patrick() (patrickIface.Client, error) {
	client, err := s.getClient(commonSpecs.Patrick)
	if err != nil {
		return nil, err
	}

	patrickClient, ok := client.(patrickIface.Client)
	if !ok {
		return nil, errors.New("client is not a patrick client")
	}

	return patrickClient, nil
}

func (s *Simple) TNS() (tnsIface.Client, error) {
	client, err := s.getClient(commonSpecs.TNS)
	if err != nil {
		return nil, err
	}

	tnsClient, ok := client.(tnsIface.Client)
	if !ok {
		return nil, errors.New("client is not a tns client")
	}

	return tnsClient, nil
}

func (s *Simple) Monkey() (monkeyIface.Client, error) {
	client, err := s.getClient(commonSpecs.Monkey)
	if err != nil {
		return nil, err
	}

	monkeyClient, ok := client.(monkeyIface.Client)
	if !ok {
		return nil, errors.New("client is not a monkey client")
	}

	return monkeyClient, nil
}

func (s *Simple) Hoarder() (hoarderIface.Client, error) {
	client, err := s.getClient(commonSpecs.Hoarder)
	if err != nil {
		return nil, err
	}

	hoarderClient, ok := client.(hoarderIface.Client)
	if !ok {
		return nil, errors.New("client is not a hoarder client")
	}

	return hoarderClient, nil
}

func (u *Universe) Simple(name string) (*Simple, error) {
	simple, ok := u.simples[name]
	if !ok {
		return nil, fmt.Errorf("Simple `%s` not found", name)
	}

	return simple, nil
}

func (u *Universe) CreateSimpleNode(name string, config *SimpleConfig) (peer.Node, error) {
	var err error

	if _, exists := u.simples[name]; exists {
		return nil, fmt.Errorf("simple Node `%s` exists in universe `%s`", name, u.Name())
	}

	if config.Port == 0 {
		config.Port = u.portShift + lastSimplePort()
	}

	simpleNode, err := peer.New(
		u.ctx,
		fmt.Sprintf("%s/simple-%s-%d", u.root, name, config.Port),
		keypair.NewRaw(),
		u.swarmKey,
		[]string{fmt.Sprintf(DefaultP2PListenFormat, config.Port)},
		[]string{fmt.Sprintf(DefaultP2PListenFormat, config.Port)},
		false,
		false,
	)
	if err != nil {
		return nil, fmt.Errorf("failed creating me error: %v", err)
	}

	simple := &Simple{Node: simpleNode, clients: make(map[string]commonIface.Client)}
	for name, clientCfg := range config.Clients {
		if err = simple.startClient(name, clientCfg); err != nil {
			return nil, fmt.Errorf("starting client `%s` failed with: %w", name, err)
		}
	}

	u.lock.Lock()
	u.simples[name] = simple
	u.lock.Unlock()

	time.Sleep(afterStartDelay())
	u.Mesh(simpleNode)
	u.Register(simpleNode, name, map[string]int{"p2p": config.Port})

	return simpleNode, nil
}
