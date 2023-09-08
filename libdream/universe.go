package libdream

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"sync"

	logging "github.com/ipfs/go-log/v2"
	ifaceCommon "github.com/taubyte/go-interfaces/common"
	peer "github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/libdream/common"
	protocols "github.com/taubyte/tau/protocols/common"
)

var (
	logger = logging.Logger("dreamland")
)

func ValidServices() []string {
	return []string{"seer", "auth", "patrick", "tns", "monkey", "hoarder", "substrate"}
}

func ValidClients() []string {
	return []string{"seer", "auth", "patrick", "tns", "monkey", "hoarder"}
}

func (u *Universe) toClose(c ifaceCommon.Service) {
	u.lock.Lock()
	defer u.lock.Unlock()
	u.closables = append(u.closables, c)
}

func (u *Universe) StartAll(simples ...string) error {
	_services := ValidServices()
	serviceMap := make(map[string]ifaceCommon.ServiceConfig, len(_services))
	for _, s := range _services {
		serviceMap[s] = ifaceCommon.ServiceConfig{}
	}

	if len(simples) == 0 {
		simples = []string{common.StartAllDefaultSimple}
	}

	simplesDef := make(map[string]SimpleConfig)
	u.lock.Lock()
	for _, name := range simples {
		simplesDef[name] = SimpleConfig{
			Clients: ClientsWithDefaults(ValidClients()...),
		}
	}
	u.lock.Unlock()

	err := u.StartWithConfig(&Config{
		Services: serviceMap,
		Simples:  simplesDef,
	})
	if err != nil {
		return err
	}

	return nil

}

func (u *Universe) GetPortHttp(node peer.Node) (int, error) {
	u.lock.RLock()
	info, ok := u.lookups[node.ID().Pretty()]
	u.lock.RUnlock()
	if !ok {
		return 0, errors.New("node does not exist")
	}

	port, ok := info.Ports["http"]
	if !ok {
		return 0, errors.New("http field does not exist")
	}

	return port, nil
}

func (u *Universe) GetURLHttp(node peer.Node) (url string, err error) {
	port, err := u.GetPortHttp(node)
	if err != nil {
		return
	}

	return fmt.Sprintf(common.DefaultHTTPListenFormat, "http", port), nil
}

func (u *Universe) GetURLHttps(node peer.Node) (url string, err error) {
	port, err := u.GetPortHttp(node)
	if err != nil {
		return
	}

	return fmt.Sprintf(common.DefaultHTTPListenFormat, "https", port), nil
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
		config.SwarmKey = protocols.SwarmKey()

		wg.Add(1)
		go func(service string, config ifaceCommon.ServiceConfig) {
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
		go func(_c ifaceCommon.Service) {
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
