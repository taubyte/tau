package libdream

import (
	"context"
	"sync"

	commonIface "github.com/taubyte/go-interfaces/common"
	ifaceCommon "github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/go-interfaces/kvdb"
	"github.com/taubyte/p2p/peer"
)

type Universe struct {
	ctx       context.Context
	ctxC      context.CancelFunc
	lock      sync.RWMutex
	name      string
	root      string
	id        string
	all       []peer.Node
	closables []commonIface.Service
	lookups   map[string]*NodeInfo
	portShift int
	service   map[string]*serviceInfo
	simples   map[string]*Simple

	keepRoot bool
}

type serviceInfo struct {
	nodes map[string]commonIface.Service
}

type Handlers struct {
	Service func(context.Context, *commonIface.ServiceConfig) (commonIface.Service, error)
	Client  func(peer.Node, *commonIface.ClientConfig) (commonIface.Client, error)
}

var Registry = struct {
	Auth      Handlers
	Hoarder   Handlers
	Monkey    Handlers
	Patrick   Handlers
	Seer      Handlers
	TNS       Handlers
	Substrate Handlers
}{}

// Order of params important!
type FixtureHandler func(universe *Universe, params ...interface{}) error

type FixtureVariable struct {
	Name        string
	Alias       string
	Description string
	Required    bool
}

type FixtureDefinition struct {
	Description string
	ImportRef   string
	Variables   []FixtureVariable
	BlockCLI    bool
	Internal    bool
}

type ClientCreationMethod func(*ifaceCommon.ClientConfig) error

type SimpleConfig struct {
	ifaceCommon.CommonConfig
	Clients SimpleConfigClients
}

type NodeInfo struct {
	DbFactory kvdb.Factory
	Node      peer.Node
	Name      string
	Ports     map[string]int
}

type SimpleConfigClients struct {
	Seer      *ifaceCommon.ClientConfig
	Auth      *ifaceCommon.ClientConfig
	Patrick   *ifaceCommon.ClientConfig
	TNS       *ifaceCommon.ClientConfig
	Monkey    *ifaceCommon.ClientConfig
	Hoarder   *ifaceCommon.ClientConfig
	Substrate *ifaceCommon.ClientConfig
}

type Config struct {
	Services map[string]ifaceCommon.ServiceConfig
	Clients  map[string]ifaceCommon.ClientConfig
	Simples  map[string]SimpleConfig
}
