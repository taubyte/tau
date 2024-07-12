package dream

import (
	"context"
	"sync"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/peer"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
)

type Universe struct {
	ctx       context.Context
	ctxC      context.CancelFunc
	lock      sync.RWMutex
	name      string
	root      string
	id        string
	swarmKey  []byte
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

type ServiceCreate func(*Universe, *commonIface.ServiceConfig) (commonIface.Service, error)
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

type Simple struct {
	peer.Node
	clients map[string]commonIface.Client
	lock    sync.RWMutex
}

// Deprecated use Map[string]*commonIface.ClientConfig{}
type SimpleConfigClients struct {
	TNS     *commonIface.ClientConfig
	Auth    *commonIface.ClientConfig
	Seer    *commonIface.ClientConfig
	Patrick *commonIface.ClientConfig
	Monkey  *commonIface.ClientConfig
	Hoarder *commonIface.ClientConfig
}

func (s SimpleConfigClients) Compat() map[string]*commonIface.ClientConfig {
	newClientConfig := make(map[string]*commonIface.ClientConfig)

	if s.TNS != nil {
		newClientConfig[commonSpecs.TNS] = s.TNS
	}

	if s.Auth != nil {
		newClientConfig[commonSpecs.Auth] = s.Auth
	}

	if s.Seer != nil {
		newClientConfig[commonSpecs.Seer] = s.Seer
	}

	if s.Patrick != nil {
		newClientConfig[commonSpecs.Patrick] = s.Patrick
	}

	if s.Monkey != nil {
		newClientConfig[commonSpecs.Monkey] = s.Monkey
	}

	if s.Hoarder != nil {
		newClientConfig[commonSpecs.Hoarder] = s.Hoarder
	}

	return newClientConfig
}
