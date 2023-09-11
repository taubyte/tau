package libdream

import (
	"context"
	"sync"

	commonIface "github.com/taubyte/go-interfaces/common"
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

type handlers struct {
	service ServiceCreate
	client  ClientCreate
}

type ServiceCreate func(context.Context, *commonIface.ServiceConfig) (commonIface.Service, error)
type ClientCreate func(peer.Node, *commonIface.ClientConfig) (commonIface.Client, error)

var Registry *handlerRegistry

type handlerRegistry struct {
	registry map[string]*handlers
	lock     sync.RWMutex
}

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

type ClientCreationMethod func(*commonIface.ClientConfig) error

type SimpleConfig struct {
	commonIface.CommonConfig
	Clients map[string]*commonIface.ClientConfig
}

type NodeInfo struct {
	DbFactory kvdb.Factory
	Node      peer.Node
	Name      string
	Ports     map[string]int
}

type Config struct {
	Services map[string]commonIface.ServiceConfig
	Clients  map[string]commonIface.ClientConfig
	Simples  map[string]SimpleConfig
}

type UniverseConfig struct {
	Name     string
	Id       string
	KeepRoot bool
}

type serviceStatus struct {
	Name   string `json:"name"`
	Copies int    `json:"copies"`
}

type Multiverse struct{}
