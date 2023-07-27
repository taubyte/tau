package services

import (
	"context"
	"fmt"
	"os"
	"path"
	"sync"

	commonIface "github.com/taubyte/go-interfaces/common"
	peerIface "github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/libdream/common"
	"github.com/taubyte/utils/id"
)

var (
	universes      map[string]*Universe
	universesLock  sync.RWMutex
	multiverseCtx  context.Context
	multiverseCtxC context.CancelFunc
)

func init() {
	universes = make(map[string]*Universe)
	multiverseCtx, multiverseCtxC = context.WithCancel(context.Background())
}

// kill them all
// ref: https://dragonball.fandom.com/wiki/Zeno
func Zeno() {
	universesLock.Lock()
	defer universesLock.Unlock()
	for _, u := range universes {
		u.Cleanup()
	}
	multiverseCtxC()
}

func NewMultiVerse() common.Multiverse {
	return &multiverse{}
}

// create or fetch a universe with a specific id
func MultiverseWithConfig(config UniverseConfig) common.Universe {
	// see if we have a ticket
	if config.Id == "" {
		config.Id = id.Generate()
	}

	universesLock.Lock()
	defer universesLock.Unlock()

	u, exists := universes[config.Name]
	if exists {
		return u
	}

	u = &Universe{
		name:      config.Name,
		id:        config.Id,
		all:       make([]peerIface.Node, 0),
		closables: make([]commonIface.Service, 0),
		simples:   make(map[string]*Simple),
		lookups:   make(map[string]*common.NodeInfo),
		portShift: LastUniversePortShift(),
		service: func() map[string]*serviceInfo {
			s := make(map[string]*serviceInfo)
			for _, srvt := range ValidServices() {
				s[srvt] = new(serviceInfo)
				s[srvt].nodes = make(map[string]commonIface.Service)
			}
			return s
		}(),

		keepRoot: config.KeepRoot,
	}
	u.ctx, u.ctxC = context.WithCancel(multiverseCtx)

	if u.keepRoot {
		cacheFolder, err := common.GetCacheFolder()
		if err != nil {
			return nil
		}

		u.root = path.Join(cacheFolder, "universe-"+u.id)
	} else {
		u.root = "/tmp/universe-" + u.id
	}

	_, err := os.Stat(u.root)
	if err != nil {
		err = os.MkdirAll(u.root, 0755)
		if err != nil {
			return nil
		}
	}

	universes[u.name] = u

	// add an elder node
	elderConfig := struct {
		Config common.SimpleConfig
	}{}

	_, err = u.CreateSimpleNode("elder", &elderConfig.Config)
	if err != nil {
		fmt.Println("Create simple failed", err)
	}

	return u
}

// create or fetch a universe
func Multiverse(name string) common.Universe {
	// see if we have a ticket

	universesLock.Lock()
	defer universesLock.Unlock()

	u, exists := universes[name]
	if exists {
		return u
	}

	u = &Universe{
		name:      name,
		id:        id.Generate(),
		all:       make([]peerIface.Node, 0),
		closables: make([]commonIface.Service, 0),
		simples:   make(map[string]*Simple),
		lookups:   make(map[string]*common.NodeInfo),
		portShift: LastUniversePortShift(),
		service: func() map[string]*serviceInfo {
			s := make(map[string]*serviceInfo)
			for _, srvt := range ValidServices() {
				s[srvt] = new(serviceInfo)
				s[srvt].nodes = make(map[string]commonIface.Service)
			}
			return s
		}(),
	}
	u.ctx, u.ctxC = context.WithCancel(multiverseCtx)
	u.root = "/tmp/universe-" + u.id

	var err error
	err = os.MkdirAll(u.root, 0755)
	if err != nil {
		return nil
	}

	universes[name] = u

	// add an elder node
	elderConfig := struct {
		Config common.SimpleConfig
	}{}

	_, err = u.CreateSimpleNode("elder", &elderConfig.Config)
	if err != nil {
		fmt.Println("Create simple failed", err)
	}

	return u
}

func Exist(name string) bool {
	universesLock.RLock()
	defer universesLock.RUnlock()
	_, exists := universes[name]
	return exists
}
