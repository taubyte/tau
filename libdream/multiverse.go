package libdream

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

func NewMultiVerse() *Multiverse {
	return &Multiverse{}
}

// create or fetch a universe
func NewUniverse(config UniverseConfig) *Universe {
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

	u = &Universe{
		name:      config.Name,
		id:        id,
		all:       make([]peerIface.Node, 0),
		closables: make([]commonIface.Service, 0),
		simples:   make(map[string]*Simple),
		lookups:   make(map[string]*NodeInfo),
		portShift: LastUniversePortShift(),
		keepRoot:  config.KeepRoot,
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

	if config.KeepRoot {
		cacheFolder, err := common.GetCacheFolder()
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

func Exist(name string) bool {
	universesLock.RLock()
	defer universesLock.RUnlock()
	_, exists := universes[name]
	return exists
}
