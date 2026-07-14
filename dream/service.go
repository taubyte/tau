package dream

import (
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	peer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/kvdb"
)

// otherPortKeys is the fixed order in which "Others" ports are assigned from
// a freshly reserved batch of ports.
var otherPortKeys = []string{"http", "p2p", "dns", "ipfs"}

func (u *Universe) createService(name string, config *commonIface.ServiceConfig) error {
	if config.Disabled {
		return nil
	}

	if config.Root == "" {
		config.Root = u.root
	}

	serviceCount := len(u.service[name].nodes)
	config.Root = path.Join(config.Root, fmt.Sprintf("%s-%d", name, serviceCount))
	// Ignoring error in case of opening
	os.MkdirAll(config.Root, 0750)

	if config.Others == nil {
		config.Others = make(map[string]int)
	}

	var (
		node peer.Node
		err  error
	)

	// Binding a service can race another process for the same kernel-picked
	// port between reservation and bind; retry a few times with a fresh batch
	// of ports rather than failing the whole service start.
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			for _, k := range otherPortKeys {
				config.Others[k] = 0
			}
		}

		var ports []int
		ports, err = GetFreePorts(len(otherPortKeys))
		if err != nil {
			return err
		}

		for i, k := range otherPortKeys {
			if prt, ok := config.Others[k]; !ok || prt == 0 {
				config.Others[k] = ports[i]

				if k == "p2p" {
					config.Port = config.Others[k]
				}
			}
		}

		node, err = u.startService(name, config)
		if err == nil {
			break
		}
		if !strings.Contains(err.Error(), "address already in use") {
			return err
		}
	}
	if err != nil {
		return err
	}

	config.Databases = kvdb.New(node)

	// we mesh first
	u.Mesh(node)

	// Wait (bounded, best-effort) until at least one peer has connected.
	// Skipping this — even for the first-booted node, which pays the full
	// timeout — lets a service make its first pubsub/DHT moves while
	// isolated; those failures get backoff-cached for 60s+ and wedge the
	// universe for minutes.
	node.WaitForSwarm(time.Second)

	// register so others can mesh with it
	u.Register(node, name, config.Others)

	return nil
}

func (u *Universe) Service(name string, config *commonIface.ServiceConfig) error {
	//make sure we have swarm key
	config.SwarmKey = u.swarmKey

	if config.Others == nil {
		config.Others = make(map[string]int)
	}

	if config.Others["copies"] <= 0 {
		config.Others["copies"] = 1
	}

	for i := 0; i < config.Others["copies"]; i++ {
		err := u.createService(name, config.Clone())
		if err != nil {
			return err
		}
	}

	return nil
}

func (u *Universe) registerService(name string, srv commonIface.Service) peer.Node {
	u.lock.Lock()
	defer u.lock.Unlock()
	registered, ok := u.service[name]
	if !ok {
		registered = &serviceInfo{
			nodes: make(map[string]commonIface.Service),
		}
		u.service[name] = registered
	}
	registered.nodes[srv.Node().ID().String()] = srv
	return srv.Node()
}

func (u *Universe) GetServicePids(name string) ([]string, error) {
	nodes, ok := u.service[name]
	if !ok {
		return nil, fmt.Errorf("%s was not found", name)
	}

	pids := make([]string, 0)
	for pid := range nodes.nodes {
		pids = append(pids, pid)
	}

	return pids, nil
}

func (h *handlerRegistry) Set(protocol string, service ServiceCreate, client ClientCreate) error {
	if protocol == "" {
		return fmt.Errorf("protocol required")
	}

	h.lock.Lock()
	defer h.lock.Unlock()

	// Create the slot on demand: services pre-seeded from commonSpecs.Services
	// already have one, but a build-tag-gated (e.g. ee) service registered from
	// an init() may not be in that list yet, so Set is what admits it.
	hs, ok := h.registry[protocol]
	if !ok {
		hs = &handlers{}
		h.registry[protocol] = hs
	}

	if service != nil {
		hs.service = service
	}

	if client != nil {
		hs.client = client
	}

	return nil
}

func (h *handlerRegistry) client(protocol string) (ClientCreate, error) {
	handlers, err := h.handlers(protocol)
	if err != nil {
		return nil, err
	}

	if handlers.client == nil {
		return nil, fmt.Errorf("client creation method is nil have you imported _ \"github.com/taubyte/tau/clients/p2p/%s/dream\"", protocol)
	}

	return handlers.client, nil
}

func (h *handlerRegistry) service(protocol string) (ServiceCreate, error) {
	handlers, err := h.handlers(protocol)
	if err != nil {
		return nil, err
	}

	if handlers.service == nil {
		return nil, fmt.Errorf("Service creation method is nil have you imported _ \"github.com/taubyte/tau/services/%s/dream\"", protocol)
	}

	return handlers.service, nil
}

func (h *handlerRegistry) handlers(protocol string) (*handlers, error) {
	h.lock.RLock()
	handlers, ok := h.registry[protocol]
	h.lock.RUnlock()
	if !ok {
		return nil, fmt.Errorf("protocol `%s` does not exist", protocol)
	}

	return handlers, nil
}
