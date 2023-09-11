package libdream

import (
	"fmt"
	"time"

	commonIface "github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/p2p/keypair"
	peer "github.com/taubyte/p2p/peer"
	protocols "github.com/taubyte/tau/protocols/common"
)

func (u *Universe) CreateSimpleNode(name string, config *SimpleConfig) (peer.Node, error) {
	var err error

	if _, exists := u.simples[name]; exists {
		return nil, fmt.Errorf("simple Node `%s` exists in universe `%s`", name, u.Name())
	}

	if config.Port == 0 {
		config.Port = u.portShift + LastSimplePortAllocated()
	}

	simpleNode, err := peer.New(
		u.ctx,
		fmt.Sprintf("%s/simple-%s-%d", u.root, name, config.Port),
		keypair.NewRaw(),
		protocols.SwarmKey(),
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
			return nil, fmt.Errorf("starting clinet `%s` failed with: %w", name, err)
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
