package dream

import (
	"errors"
	"fmt"
	"strings"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	authIface "github.com/taubyte/tau/core/services/auth"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	monkeyIface "github.com/taubyte/tau/core/services/monkey"
	patrickIface "github.com/taubyte/tau/core/services/patrick"
	seerIface "github.com/taubyte/tau/core/services/seer"
	tnsIface "github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/p2p/keypair"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
	"golang.org/x/exp/slices"

	peerCore "github.com/libp2p/go-libp2p/core/peer"

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

func (s *Simple) Accounts() (accountsIface.Client, error) {
	client, err := s.getClient(commonSpecs.Accounts)
	if err != nil {
		return nil, err
	}

	accountsClient, ok := client.(accountsIface.Client)
	if !ok {
		return nil, errors.New("client is not an accounts client")
	}

	return accountsClient, nil
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

	// only retry with a fresh port if we're the one who allocated it; an
	// explicit config.Port is a caller's choice, not ours to change
	allocatedPort := config.Port == 0
	if allocatedPort {
		ports, ferr := GetFreePorts(1)
		if ferr != nil {
			return nil, ferr
		}
		config.Port = ports[0]
	}

	upeers := u.Peers()
	bpeers := make([]peerCore.AddrInfo, 0, len(upeers))
	for _, n := range upeers {
		if pi, err := peerCore.AddrInfoFromP2pAddr(n.Peer().Addrs()[0]); err == nil {
			bpeers = append(bpeers, *pi)
		}
	}

	var simpleNode peer.Node
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			ports, perr := GetFreePorts(1)
			if perr != nil {
				return nil, perr
			}
			config.Port = ports[0]
		}

		simpleNode, err = peer.NewLitePublic(
			u.ctx,
			fmt.Sprintf("%s/simple-%s-%d", u.root, name, config.Port),
			keypair.NewRaw(),
			u.swarmKey,
			[]string{fmt.Sprintf(DefaultP2PListenFormat, config.Port)},
			[]string{fmt.Sprintf(DefaultP2PListenFormat, config.Port)},
			peer.BootstrapParams{Enable: len(bpeers) > 0, Peers: bpeers},
		)
		if err == nil {
			break
		}
		if !allocatedPort || !strings.Contains(err.Error(), "address already in use") {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("failed creating me error: %v", err)
	}

	simple := &Simple{Node: simpleNode, clients: make(map[string]commonIface.Client)}
	for name, clientCfg := range config.Clients {
		// make sure the client asked for is a valid client
		if slices.Contains(commonSpecs.Clients, name) {
			if err = simple.startClient(name, clientCfg); err != nil {
				return nil, fmt.Errorf("starting client `%s` failed with: %w", name, err)
			}
		}
	}

	u.lock.Lock()
	u.simples[name] = simple
	u.lock.Unlock()

	// Settle before joining the mesh. Meshing the simple immediately after
	// creation deterministically wedges parallel universe boots (services
	// end up deaf to each other for minutes); the exact libp2p-level race
	// is not pinned down, so this keeps the settle main always had, made
	// deterministic. Do not remove without a green full dreaming sweep.
	time.Sleep(750 * time.Millisecond)
	u.Mesh(simpleNode)
	u.Register(simpleNode, name, map[string]int{"p2p": config.Port})

	return simpleNode, nil
}
