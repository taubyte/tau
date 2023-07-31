package services

import (
	"fmt"
	"os"
	"path"
	"time"

	commonIface "github.com/taubyte/go-interfaces/common"
	peer "github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/libdream/common"
	"github.com/taubyte/tau/pkgs/kvdb"
)

func (u *Universe) PortFor(proto, _type string) int {
	serviceCount := len(u.service[proto].nodes)
	switch _type {
	case "http":
		return u.portShift + getHttpPort(proto) + serviceCount
	case "p2p":
		return u.portShift + getP2pPort(proto) + serviceCount
	case "dns":
		return u.portShift + common.DefaultDnsPort + serviceCount
	}
	return -1
}

func (u *Universe) createService(name string, config *commonIface.ServiceConfig) error {
	if config.Root == "" {
		config.Root = u.root
	}

	serviceCount := len(u.service[name].nodes)
	config.Root = path.Join(config.Root, fmt.Sprintf("%s-%d", name, serviceCount), u.id)
	// Ignoring error in case of opening
	os.MkdirAll(config.Root, 0750)

	if config.Others == nil {
		config.Others = make(map[string]int)
	}

	for _, k := range []string{"http", "p2p", "dns"} {
		if _, ok := config.Others[k]; !ok {
			config.Others[k] = u.PortFor(name, k)
			if k == "p2p" {
				config.Port = config.Others[k]
			}
		}
	}

	all := map[string]func(*commonIface.ServiceConfig) (peer.Node, error){
		"auth":      u.CreateAuthService,
		"hoarder":   u.CreateHoarderService,
		"monkey":    u.CreateMonkeyService,
		"patrick":   u.CreatePatrickService,
		"seer":      u.CreateSeerService,
		"tns":       u.CreateTNSService,
		"substrate": u.CreateSubstrateService,
	}

	handle, ok := all[name]
	if !ok {
		return fmt.Errorf("service `%s` does not exist", name)
	}

	if config.Disabled {
		return nil
	}

	node, err := handle(config)
	if err != nil {
		return err
	}

	config.Databases = kvdb.New(node)

	// we mesh first
	u.Mesh(node)
	// wait till we're connected to others
	node.WaitForSwarm(common.AfterStartDelay())
	// register so others can mesh with it
	u.Register(node, name, config.Others)
	time.Sleep(common.AfterStartDelay())

	return nil
}

func (u *Universe) Service(name string, config *commonIface.ServiceConfig) error {
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
	registered.nodes[srv.Node().ID().Pretty()] = srv
	return srv.Node()
}

func (u *Universe) GetServicePids(name string) ([]string, error) {
	var pids []string
	nodes, ok := u.service[name]
	if !ok {
		return nil, fmt.Errorf("%s was not found", name)
	}

	for pid := range nodes.nodes {
		pids = append(pids, pid)
	}

	return pids, nil
}

func getHttpPort(name string) int {
	switch name {
	case "auth":
		return common.DefaultAuthHttpPort
	case "substrate":
		return common.DefaultSubstrateHttpPort
	case "patrick":
		return common.DefaultPatrickHttpPort
	case "seer":
		return common.DefaultSeerHttpPort
	case "tns":
		return common.DefaultTNSHttpPort
	}
	return 0
}

func getP2pPort(name string) int {
	switch name {
	case "auth":
		return common.DefaultAuthPort
	case "hoarder":
		return common.DefaultHoarderPort
	case "monkey":
		return common.DefaultMonkeyPort
	case "substrate":
		return common.DefaultSubstratePort
	case "patrick":
		return common.DefaultPatrickPort
	case "seer":
		return common.DefaultSeerPort
	case "tns":
		return common.DefaultTNSPort
	}
	return 0
}
