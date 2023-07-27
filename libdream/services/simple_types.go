package services

import (
	"fmt"

	authIface "github.com/taubyte/go-interfaces/services/auth"
	hoarderIface "github.com/taubyte/go-interfaces/services/hoarder"
	monkeyIface "github.com/taubyte/go-interfaces/services/monkey"
	patrickIface "github.com/taubyte/go-interfaces/services/patrick"
	seerIface "github.com/taubyte/go-interfaces/services/seer"
	tnsIface "github.com/taubyte/go-interfaces/services/tns"
	p2p "github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/libdream/common"
)

type Simple struct {
	p2p.Node
	Clients struct {
		seer    seerIface.Client
		auth    authIface.Client
		patrick patrickIface.Client
		tns     tnsIface.Client
		monkey  monkeyIface.Client
		hoarder hoarderIface.Client
	}
}

func (u *Universe) Simple(name string) (common.Simple, error) {
	simple, ok := u.simples[name]
	if !ok {
		return nil, fmt.Errorf("Simple `%s` not found", name)
	}

	return simple, nil
}

func (s *Simple) PeerNode() p2p.Node {
	return s.Node
}

func (s *Simple) Seer() seerIface.Client {
	return s.Clients.seer
}

func (s *Simple) Auth() authIface.Client {
	return s.Clients.auth
}

func (s *Simple) Patrick() patrickIface.Client {
	return s.Clients.patrick
}

func (s *Simple) TNS() tnsIface.Client {
	return s.Clients.tns
}

func (s *Simple) Monkey() monkeyIface.Client {
	return s.Clients.monkey
}

func (s *Simple) Hoarder() hoarderIface.Client {
	return s.Clients.hoarder
}
