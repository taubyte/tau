package dream

import (
	"fmt"
	"os"
	"path"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	peer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/kvdb"
)

func (u *Universe) PortFor(proto, _type string) (int, error) {
	serviceCount := len(u.service[proto].nodes)
	var mapPath string
	switch _type {
	case "http", "p2p", "ipfs", "dns":
		mapPath = _type + "/" + proto
	default:
		return 0, fmt.Errorf("invalid type `%s`", _type)
	}

	port, ok := Ports[mapPath]
	if !ok {
		return 0, fmt.Errorf("no port set for type `%s` protocol `%s`", _type, proto)
	}

	return u.portShift + port + serviceCount, nil
}

func (u *Universe) createService(name string, config *commonIface.ServiceConfig) error {
	if config.Root == "" {
		config.Root = path.Clean(fmt.Sprintf("%s/%s/%d", u.root, name, len(u.service[name].nodes)))
	}

	serviceCount := len(u.service[name].nodes)
	config.Root = path.Join(config.Root, fmt.Sprintf("%s-%d", name, serviceCount), u.id)
	// Ignoring error in case of opening
	os.MkdirAll(config.Root, 0750)

	os.Mkdir(config.Root+"/storage", 0755)

	if config.Others == nil {
		config.Others = make(map[string]int)
	}

	var err error
	for _, k := range []string{"http", "p2p", "dns", "ipfs"} {
		if prt, ok := config.Others[k]; !ok || prt == 0 {
			config.Others[k], _ = u.PortFor(name, k)

			if k == "p2p" {
				config.Port = config.Others[k]
			}
		}
	}

	if config.Disabled {
		return nil
	}

	node, err := u.startService(name, config)
	if err != nil {
		return err
	}

	config.Databases = kvdb.New(node)

	// we mesh first
	u.Mesh(node)

	// wait till we're connected to others
	node.WaitForSwarm(afterStartDelay())

	// register so others can mesh with it
	u.Register(node, name, config.Others)
	time.Sleep(afterStartDelay())

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
		u.service[name] = &serviceInfo{
			nodes: make(map[string]commonIface.Service),
		}
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
	handlers, err := h.handlers(protocol)
	if err != nil {
		return err
	}

	h.lock.Lock()
	defer h.lock.Unlock()
	if service != nil {
		handlers.service = service
	}

	if client != nil {
		handlers.client = client
	}

	return nil
}

func (h *handlerRegistry) client(protocol string) (ClientCreate, error) {
	handlers, err := h.handlers(protocol)
	if err != nil {
		return nil, err
	}

	if handlers.client == nil {
		return nil, fmt.Errorf("client creation method is nil have you imported _ \"github.com/taubyte/tau/services/%s\"", protocol)
	}

	return handlers.client, nil
}

func (h *handlerRegistry) service(protocol string) (ServiceCreate, error) {
	handlers, err := h.handlers(protocol)
	if err != nil {
		return nil, err
	}

	if handlers.service == nil {
		return nil, fmt.Errorf("Service creation method is nil have you imported _ \"github.com/taubyte/tau/services/%s\"", protocol)
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
