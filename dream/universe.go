package dream

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	peer "github.com/taubyte/tau/p2p/peer"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/utils"
	"golang.org/x/exp/slices"

	"github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/utils/id"
)

func (u *Universe) init() error {
	u.ctx, u.ctxC = context.WithCancel(u.multiverse.ctx)

	u.all = make([]peer.Node, 0)
	u.closables = make([]commonIface.Service, 0)
	u.simples = make(map[string]*Simple)
	u.lookups = make(map[string]*NodeInfo)
	u.service = make(map[string]*serviceInfo, len(commonSpecs.Services))
	for _, srvt := range commonSpecs.Services {
		u.service[srvt] = &serviceInfo{
			nodes: make(map[string]commonIface.Service),
		}
	}

	return nil
}

// create or fetch a universe
func (m *Multiverse) New(config UniverseConfig) *Universe {
	// make name lowercase
	config.Name = strings.ToLower(config.Name)

	// see if we have a ticket
	id := id.Generate()
	if len(config.Id) > 0 {
		id = config.Id
	}

	m.universesLock.Lock()
	defer m.universesLock.Unlock()

	u, exists := m.universes[config.Name]
	if exists {
		return u
	}

	// needs to be predictable for when KeepRoot==true
	swarmKey, _ := utils.FormatSwarmKey(utils.GenerateSwarmKeyFromString(id))

	u = &Universe{
		multiverse: m,
		name:       config.Name,
		id:         id,
		swarmKey:   swarmKey,
		portShift:  lastPortShift(),
		keepRoot:   config.KeepRoot,
	}

	if err := u.init(); err != nil {
		return nil
	}

	if config.KeepRoot {
		cacheFolder, err := GetCacheFolder()
		if err != nil {
			return nil
		}

		u.root = path.Join(cacheFolder, "universe-"+u.name)
	} else {
		u.root = "/tmp/universe-" + u.id
	}

	err := os.MkdirAll(u.root, 0755)
	if err != nil {
		return nil
	}

	m.universes[config.Name] = u

	return u
}

func (u *Universe) toClose(c commonIface.Service) {
	u.lock.Lock()
	defer u.lock.Unlock()
	u.closables = append(u.closables, c)
}

func (u *Universe) Register(node peer.Node, name string, ports map[string]int) {
	u.lock.Lock()
	defer u.lock.Unlock()
	u.lookups[node.ID().String()] = &NodeInfo{
		DbFactory: kvdb.New(node),
		Node:      node,
		Name:      name,
		Ports:     ports,
	}
}

func (u *Universe) StartAll(simples ...string) error {
	serviceMap := make(map[string]commonIface.ServiceConfig, len(commonSpecs.Services))
	for _, s := range commonSpecs.Services {
		serviceMap[s] = commonIface.ServiceConfig{}
	}

	if len(simples) < 1 {
		simples = append(simples, startAllDefaultSimple)
	}

	simplesDef := make(map[string]SimpleConfig)
	u.lock.Lock()
	for _, name := range simples {
		simplesDef[name] = SimpleConfig{
			Clients: u.defaultClients(),
		}
	}
	u.lock.Unlock()

	return u.StartWithConfig(
		&Config{
			Services: serviceMap,
			Simples:  simplesDef,
		},
	)
}

func (u *Universe) Peers() []peer.Node {
	u.lock.RLock()
	defer u.lock.RUnlock()

	nodes := make([]peer.Node, 0, len(u.lookups))
	for _, ni := range u.lookups {
		nodes = append(nodes, ni.Node)
	}

	return nodes
}

func (u *Universe) GetInfo(node peer.Node) (*NodeInfo, error) {
	u.lock.RLock()
	defer u.lock.RUnlock()
	info, ok := u.lookups[node.ID().String()]
	if !ok {
		return nil, errors.New("node does not exist")
	}
	return info, nil
}

func (u *Universe) GetPortHttp(node peer.Node) (int, error) {
	info, err := u.GetInfo(node)
	if err != nil {
		return 0, err
	}

	port, ok := info.Ports["http"]
	if !ok {
		return 0, errors.New("http field does not exist")
	}

	return port, nil
}

func (u *Universe) GetPort(node peer.Node, proto string) (int, error) {
	info, err := u.GetInfo(node)
	if err != nil {
		return 0, err
	}

	port, ok := info.Ports[proto]
	if !ok {
		return 0, errors.New(proto + " field does not exist")
	}

	return port, nil
}

func (u *Universe) getHttpUrl(node peer.Node, scheme string) (string, error) {
	if port, err := u.GetPortHttp(node); err != nil {
		return "", err
	} else {
		return fmt.Sprintf(DefaultHTTPListenFormat, scheme, port), nil
	}
}

func (u *Universe) GetURLHttp(node peer.Node) (string, error) {
	return u.getHttpUrl(node, "http")
}

func (u *Universe) GetURLHttps(node peer.Node) (string, error) {
	return u.getHttpUrl(node, "https")
}

func (u *Universe) getFixture(name string) (FixtureHandler, error) {
	fixturesLock.RLock()
	defer fixturesLock.RUnlock()
	fixtureHandler, exist := fixtures[name]
	if !exist {
		importRequired, ok := FixtureMap[name]
		if !ok {
			return nil, fmt.Errorf("fixture %s does not exist", name)
		}
		return nil, fmt.Errorf("fixture `%s` is nil, have you imported `%s`", name, importRequired.ImportRef)
	}

	return fixtureHandler, nil
}

func (u *Universe) RunFixture(name string, params ...interface{}) error {
	fixtureHandler, err := u.getFixture(name)
	if err != nil {
		return fmt.Errorf("failed getting fixture %s error: %w", name, err)
	}

	if err := fixtureHandler(u, params...); err != nil {
		return fmt.Errorf("failed running handler error: %w", err)
	}

	return nil
}

// Start universe based on config
func (u *Universe) StartWithConfig(mainConfig *Config) error {
	errChan := make(chan error, len(mainConfig.Services)+len(mainConfig.Simples))

	privKey, pubKey, err := generateDeterministicDVKeys(u.name)
	if err != nil {
		return err
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}

	var wg sync.WaitGroup
	for service, config := range mainConfig.Services {
		logger.Infof("Service %s with config:%#v\n", service, config)

		// domain validation keys
		config.PrivateKey = privKey
		config.PublicKey = pubKey

		wg.Add(1)
		go func(service string, config commonIface.ServiceConfig) {
			defer wg.Done()
			err := u.Service(service, &config)
			if err != nil {
				errChan <- fmt.Errorf("starting service `%s` failed with: %s", service, err)
			}
		}(service, config)
	}

	for name, config := range mainConfig.Simples {
		logger.Infof("Simple %s with config:%#v\n", name, config)
		if !config.Disabled {
			wg.Add(1)
			go func(name string, config SimpleConfig) {
				defer wg.Done()
				_, err := u.CreateSimpleNode(name, &config)
				if err != nil {
					errChan <- fmt.Errorf("starting simple `%s` failed with: %s", name, err)
				}
			}(name, config)
		}
	}

	wg.Wait()

	close(errChan)

	if len(errChan) > 0 {
		var errString string
		for _err := range errChan {
			errString += "\n" + _err.Error()
		}
		return errors.New(errString)
	}

	return nil
}

// compatibility
func (u *Universe) Stop() {
	u.Cleanup()

	// reset universe
	u.lock.Lock()
	defer u.lock.Unlock()

	if !u.keepRoot {
		u.multiverse.universesLock.Lock()
		defer u.multiverse.universesLock.Unlock()
		delete(u.multiverse.universes, u.name)
		return
	}

	// reset universe
	u.init()

}

func (u *Universe) Cleanup() {
	u.lock.RLock()
	defer u.lock.RUnlock()

	validPorts := []string{"http", "p2p", "ipfs", "dns"}
	// collect all used ports
	usedPorts := make(map[int]bool)
	for _, nodeInfo := range u.lookups {
		for proto, port := range nodeInfo.Ports {
			if slices.Contains(validPorts, proto) {
				usedPorts[port] = true
			}
		}
	}

	var (
		closeableWg sync.WaitGroup
		simpleWg    sync.WaitGroup
	)

	closeableWg.Add(len(u.closables))
	for _, c := range u.closables {
		go func(_c commonIface.Service) {
			_c.Close()
			_c.Node().Close()
			closeableWg.Done()
		}(c)
	}

	// close simple nodes
	simpleWg.Add(len(u.simples))
	for _, s := range u.simples {
		go func(_s *Simple) {
			_s.Close()
			_s.PeerNode().Close()
			simpleWg.Done()
		}(s)
	}

	closeableWg.Wait()
	simpleWg.Wait()

	u.ctxC()

	// wait for all usedports to be closed
	for port := range usedPorts {
		for {
			// Try to bind to the port to check if it's available
			if listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", DefaultHost, port)); err == nil {
				listener.Close()
				break // Port is available, move to next
			}
			// Port still in use, wait a bit and retry
			time.Sleep(10 * time.Millisecond)
		}
	}

}

func (u *Universe) Id() string {
	return u.id
}

func (u *Universe) Root() string {
	return u.root
}

func (u *Universe) Persistent() bool {
	return u.keepRoot
}

func (u *Universe) SwarmKey() []byte {
	return u.swarmKey
}
