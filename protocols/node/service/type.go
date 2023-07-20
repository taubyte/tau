package service

import (
	"context"

	moodyCommon "github.com/taubyte/go-interfaces/moody"
	"github.com/taubyte/go-interfaces/services/substrate"
	iface "github.com/taubyte/go-interfaces/services/substrate"
	databaseIface "github.com/taubyte/go-interfaces/services/substrate/components/database"
	httpIface "github.com/taubyte/go-interfaces/services/substrate/components/http"
	ipfsIface "github.com/taubyte/go-interfaces/services/substrate/components/ipfs"
	p2pIface "github.com/taubyte/go-interfaces/services/substrate/components/p2p"
	pubSubIface "github.com/taubyte/go-interfaces/services/substrate/components/pubsub"
	storageIface "github.com/taubyte/go-interfaces/services/substrate/components/storage"
	"github.com/taubyte/go-interfaces/services/tns"
	"github.com/taubyte/go-interfaces/vm"

	commonIface "github.com/taubyte/go-interfaces/services/common"
	http "github.com/taubyte/http"
	"github.com/taubyte/p2p/peer"
)

var _ iface.Service = &Service{}

var _ commonIface.Config = &Config{}

type Config struct {
	commonIface.GenericConfig `yaml:"z,omitempty"`
}

type Service struct {
	ctx          context.Context
	node         peer.Node
	http         http.Service
	vm           vm.Service
	nodeHttp     httpIface.Service
	nodePubSub   pubSubIface.Service
	nodeIpfs     ipfsIface.Service
	nodeDatabase databaseIface.Service
	nodeStorage  storageIface.Service
	nodeP2P      p2pIface.Service
	nodeCounters substrate.CounterService
	nodeSmartOps substrate.SmartOpsService
	dev          bool

	tns tns.Client

	branch string

	orbitals []vm.Plugin
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

func (s *Service) Logger() moodyCommon.Logger {
	return logger
}

func (s *Service) Counter() substrate.CounterService {
	return s.nodeCounters
}

func (s *Service) SmartOps() substrate.SmartOpsService {
	return s.nodeSmartOps
}

func (s *Service) Tns() tns.Client {
	return s.tns
}

func (s *Service) Branch() string {
	return s.branch
}

func (s *Service) P2P() p2pIface.Service {
	return s.nodeP2P
}
