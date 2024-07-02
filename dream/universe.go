package dream

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path"
	"sync"

	commonIface "github.com/taubyte/tau/core/common"
	peer "github.com/taubyte/tau/p2p/peer"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/utils"

	"github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/utils/id"
)

// create or fetch a universe
func New(config UniverseConfig) *Universe {
	// see if we have a ticket
	id := id.Generate()
	if len(config.Id) > 0 {
		id = config.Id
	}

	universesLock.Lock()
	defer universesLock.Unlock()

	u, exists := universes[config.Name]
	if exists {
		return u
	}

	// needs to be predictable for when KeepRoot==true
	swarmKey, _ := utils.FormatSwarmKey(utils.GenerateSwarmKeyFromString(id))

	u = &Universe{
		name:      config.Name,
		id:        id,
		swarmKey:  swarmKey,
		all:       make([]peer.Node, 0),
		closables: make([]commonIface.Service, 0),
		simples:   make(map[string]*Simple),
		lookups:   make(map[string]*NodeInfo),
		portShift: lastPortShift(),
		keepRoot:  config.KeepRoot,
		service: func() map[string]*serviceInfo {
			s := make(map[string]*serviceInfo)
			for _, srvt := range commonSpecs.Services {
				s[srvt] = new(serviceInfo)
				s[srvt].nodes = make(map[string]commonIface.Service)
			}
			return s
		}(),
	}
	u.ctx, u.ctxC = context.WithCancel(multiverseCtx)

	if config.KeepRoot {
		cacheFolder, err := getCacheFolder()
		if err != nil {
			return nil
		}

		u.root = path.Join(cacheFolder, "universe-"+u.id)
	} else {
		u.root = "/tmp/universe-" + u.id
	}

	err := os.MkdirAll(u.root, 0755)
	if err != nil {
		return nil
	}

	universes[config.Name] = u

	// add an elder node
	elderConfig := struct {
		Config SimpleConfig
	}{}

	_, err = u.CreateSimpleNode("elder", &elderConfig.Config)
	if err != nil {
		fmt.Println("Create simple failed", err)
	}

	return u
}

func GetUniverse(name string) (*Universe, error) {
	universesLock.RLock()
	defer universesLock.RUnlock()
	universe, ok := universes[name]
	if !ok {
		return nil, fmt.Errorf("universe `%s` does not exist", name)
	}

	return universe, nil
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

func (u *Universe) RunFixture(name string, params ...interface{}) error {
	fixturesLock.RLock()
	defer fixturesLock.RUnlock()
	fixtureHandler, exist := fixtures[name]
	if !exist {
		importRequired, ok := FixtureMap[name]
		if !ok {
			return fmt.Errorf("fixture %s does not exist", name)
		}
		return fmt.Errorf("fixture `%s` is nil, have you imported _ \"github.com/taubyte/%s\"", name, importRequired.ImportRef)
	}

	if err := fixtureHandler(u, params...); err != nil {
		return fmt.Errorf("failed running handler error: %w", err)
	}

	return nil
}

// Start universe based on config
func (u *Universe) StartWithConfig(mainConfig *Config) error {
	errChan := make(chan error, len(mainConfig.Services)+len(mainConfig.Simples))

	privKey, pubKey, err := generateDVKeys()
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

	u, exists := universes[u.name]
	if exists {
		for k := range universes {
			if k == u.name {
				u.lock.Lock()
				u.ctxC()
				delete(universes, k)
				u.lock.Unlock()
			}
		}
	}
}

func (u *Universe) Cleanup() {
	u.lock.RLock()
	defer u.lock.RUnlock()

	// close services

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

	if u.root != "" && !u.keepRoot {
		os.RemoveAll(u.root)
	}
}

func (u *Universe) Id() string {
	return u.id
}

func (u *Universe) SwarmKey() []byte {
	return u.swarmKey
}
