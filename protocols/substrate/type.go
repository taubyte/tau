package substrate

import (
	"context"

	"github.com/taubyte/go-interfaces/kvdb"
	iface "github.com/taubyte/go-interfaces/services/substrate"
	databaseIface "github.com/taubyte/go-interfaces/services/substrate/components/database"
	ipfsIface "github.com/taubyte/go-interfaces/services/substrate/components/ipfs"
	p2pIface "github.com/taubyte/go-interfaces/services/substrate/components/p2p"
	pubSubIface "github.com/taubyte/go-interfaces/services/substrate/components/pubsub"
	storageIface "github.com/taubyte/go-interfaces/services/substrate/components/storage"
	"github.com/taubyte/go-interfaces/services/tns"
	"github.com/taubyte/go-interfaces/vm"
	streams "github.com/taubyte/p2p/streams/service"
	httpIface "github.com/taubyte/tau/protocols/substrate/components/http"

	http "github.com/taubyte/http"
	"github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/config"
)

var _ iface.Service = &Service{}

type Config struct {
	config.Node `yaml:"z,omitempty"`
}

// TODO: Node shouldn't have to have all
type Service struct {
	ctx        context.Context
	node       peer.Node
	http       http.Service
	vm         vm.Service
	components components

	dev       bool
	verbose   bool
	databases kvdb.Factory
	stream    *streams.CommandService

	tns      tns.Client
	orbitals []vm.Plugin

	cpuCount   int
	cpuAverage float64
}

// TODO: All of these components interfaces can be removed
type components struct {
	http     *httpIface.Service
	pubsub   pubSubIface.Service
	ipfs     ipfsIface.Service
	database databaseIface.Service
	storage  storageIface.Service
	p2p      p2pIface.Service
	counters iface.CounterService
	smartops iface.SmartOpsService
}

func (n *Service) Context() context.Context {
	return n.ctx
}

func (n *Service) Node() peer.Node {
	return n.node
}

func (s *Service) Vm() vm.Service {
	return s.vm
}

func (s *Service) Http() http.Service {
	return s.http
}

func (s *Service) Counter() iface.CounterService {
	return s.components.counters
}

func (s *Service) SmartOps() iface.SmartOpsService {
	return s.components.smartops
}

func (s *Service) Tns() tns.Client {
	return s.tns
}

func (s *Service) P2P() p2pIface.Service {
	return s.components.p2p
}
