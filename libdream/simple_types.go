package libdream

import (
	"errors"
	"fmt"
	"sync"

	commonIface "github.com/taubyte/go-interfaces/common"
	authIface "github.com/taubyte/go-interfaces/services/auth"
	hoarderIface "github.com/taubyte/go-interfaces/services/hoarder"
	monkeyIface "github.com/taubyte/go-interfaces/services/monkey"
	patrickIface "github.com/taubyte/go-interfaces/services/patrick"
	seerIface "github.com/taubyte/go-interfaces/services/seer"
	tnsIface "github.com/taubyte/go-interfaces/services/tns"
	commonSpecs "github.com/taubyte/go-specs/common"
	p2p "github.com/taubyte/p2p/peer"
)

type Simple struct {
	p2p.Node
	clients map[string]commonIface.Client
	lock    sync.RWMutex
}

func (u *Universe) Simple(name string) (*Simple, error) {
	simple, ok := u.simples[name]
	if !ok {
		return nil, fmt.Errorf("Simple `%s` not found", name)
	}

	return simple, nil
}

func (s *Simple) getClient(name string) (commonIface.Client, error) {
	s.lock.RLock()
	client, ok := s.clients[name]
	s.lock.RUnlock()
	if !ok {
		return nil, fmt.Errorf("client for protocol `%s` does not exist", name)
	}

	return client, nil
}

func (s *Simple) PeerNode() p2p.Node {
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
