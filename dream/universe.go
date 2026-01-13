package dream

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"path/filepath"
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

	u.state = UniverseStateStopped

	return nil
}

// create or fetch a universe
func (m *Multiverse) New(config UniverseConfig) (*Universe, error) {
	// make name lowercase
	config.Name = strings.ToLower(config.Name)

	// if no id, we use the name to generate a deterministic id
	if len(config.Id) == 0 {
		if config.KeepRoot {
			config.Id = id.GenerateDeterministic(config.Name)
		} else {
			config.Id = id.Generate(config.Name)
		}
	}

	id := config.Id

	m.universesLock.Lock()
	defer m.universesLock.Unlock()

	u, exists := m.universes[config.Name]
	if exists {
		return u, nil
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
		return nil, err
	}

	if config.KeepRoot {
		cacheFolder, err := GetCacheFolder(m.name)
		if err != nil {
			return nil, err
		}

		u.root = path.Join(cacheFolder, "universe-"+u.name)
	} else {
		u.root = filepath.Join(os.TempDir(), "universe-"+u.id)
	}

	err := os.MkdirAll(u.root, 0755)
	if err != nil {
		return nil, err
	}

	// Initialize disk usage cache as empty since we just created the directory
	u.setDiskUsageCache(0)

	m.universes[config.Name] = u

	return u, nil
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
	// Check if we can start (must be in Stopped state)
	if !u.Stopped() {
		return fmt.Errorf("universe is not in stopped state, current state: %v", u.State())
	}

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
	// Check if we can start (must be in Stopped state)
	if !u.Stopped() {
		return fmt.Errorf("universe is not in stopped state, current state: %v", u.State())
	}

	// Set state to starting
	u.lock.Lock()
	u.state = UniverseStateStarting
	u.lock.Unlock()

	errChan := make(chan error, len(mainConfig.Services)+len(mainConfig.Simples))

	privKey, pubKey, err := generateDeterministicDVKeys(u.name)
	if err != nil {
		u.lock.Lock()
		u.state = UniverseStateStopped // Reset to stopped on error
		u.lock.Unlock()
		return err
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		u.lock.Lock()
		u.state = UniverseStateStopped // Reset to stopped on error
		u.lock.Unlock()
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

	// Set state to running (even if there are errors, services are started)
	u.lock.Lock()
	u.state = UniverseStateRunning
	u.lock.Unlock()

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
	// Check if we can stop (must be in Running state)
	if !u.Running() {
		return // Already stopped or in invalid state
	}

	// Set state to stopping
	u.lock.Lock()
	u.state = UniverseStateStopping
	u.lock.Unlock()

	u.Cleanup()

	u.lock.Lock()
	defer u.lock.Unlock()

	// Transition to stopped state
	u.state = UniverseStateStopped

	// if not persistent, we delete the universe from the multiverse
	if !u.keepRoot {
		u.multiverse.universesLock.Lock()
		defer u.multiverse.universesLock.Unlock()
		delete(u.multiverse.universes, u.name)
		return
	}

	// reset universe if persistent
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
			closeableWg.Done()
		}(c)
	}

	simpleWg.Add(len(u.simples))
	for _, s := range u.simples {
		go func(_s *Simple) {
			_s.Close()
			simpleWg.Done()
		}(s)
	}

	closeableWg.Wait()
	simpleWg.Wait()

	u.ctxC()

	time.Sleep(300 * time.Millisecond)

	var nodeWg sync.WaitGroup
	nodeWg.Add(len(u.closables) + len(u.simples))
	for _, c := range u.closables {
		go func(_c commonIface.Service) {
			_c.Node().Close()
			nodeWg.Done()
		}(c)
	}
	for _, s := range u.simples {
		go func(_s *Simple) {
			_s.PeerNode().Close()
			nodeWg.Done()
		}(s)
	}
	nodeWg.Wait()

	cleanupCtx, cleanupCtxCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cleanupCtxCancel()

	// wait for all usedports to be closed
	for port := range usedPorts {
		select {
		case <-cleanupCtx.Done():
			return
		default:
		}
		for {
			select {
			case <-cleanupCtx.Done():
				return
			default:
			}
			// Try to bind to the port to check if it's available (both TCP and UDP)
			tcpListener, tcpErr := net.Listen("tcp", fmt.Sprintf("%s:%d", DefaultHost, port))
			udpAddr, udpErr := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", DefaultHost, port))
			var udpConn *net.UDPConn
			if udpErr == nil {
				udpConn, udpErr = net.ListenUDP("udp", udpAddr)
			}

			if tcpErr == nil && udpErr == nil {
				tcpListener.Close()
				udpConn.Close()
				break // Port is available, move to next
			}

			if tcpListener != nil {
				tcpListener.Close()
			}
			if udpConn != nil {
				udpConn.Close()
			}

			// Port still in use, wait a bit and retry
			time.Sleep(10 * time.Millisecond)
		}
	}

}

func (u *Universe) DiskUsage() (int64, error) {
	// Check if cache is still valid
	if cachedSize, valid := u.getDiskUsageCache(); valid {
		return cachedSize, nil
	}

	// Cache is invalid or doesn't exist, calculate disk usage
	totalSize, err := u.calculateDiskUsage()
	if err != nil {
		return 0, err
	}

	// Update cache
	u.setDiskUsageCache(totalSize)
	return totalSize, nil
}

// getDiskUsageCache returns the cached disk usage and whether it's still valid
func (u *Universe) getDiskUsageCache() (int64, bool) {
	u.diskUsageCacheLock.RLock()
	defer u.diskUsageCacheLock.RUnlock()

	if u.diskUsageCacheTime.IsZero() || time.Since(u.diskUsageCacheTime) >= DiskUsageCacheTimeout {
		return 0, false
	}

	return u.diskUsageCache, true
}

// setDiskUsageCache updates the disk usage cache with the given value
func (u *Universe) setDiskUsageCache(size int64) {
	u.diskUsageCacheLock.Lock()
	defer u.diskUsageCacheLock.Unlock()

	u.diskUsageCache = size
	u.diskUsageCacheTime = time.Now()
}

// calculateDiskUsage performs the actual disk usage calculation
func (u *Universe) calculateDiskUsage() (int64, error) {
	var totalSize int64

	err := filepath.WalkDir(u.root, func(path string, fileInfo os.DirEntry, err error) error {
		if err != nil {
			// Skip files/directories we can't access
			return nil
		}

		if !fileInfo.IsDir() {
			info, err := fileInfo.Info()
			if err != nil {
				// Skip files we can't get info for
				return nil
			}
			totalSize += info.Size()
		}

		return nil
	})

	if err != nil {
		return 0, fmt.Errorf("failed to walk directory %s: %w", u.root, err)
	}

	return totalSize, nil
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
