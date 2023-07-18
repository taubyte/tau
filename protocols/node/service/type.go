package service

import (
	"context"

	logging "github.com/ipfs/go-log/v2"
	peer "github.com/taubyte/go-interfaces/p2p/peer"
	commonIface "github.com/taubyte/go-interfaces/services/common"
	http "github.com/taubyte/go-interfaces/services/http"
	iface "github.com/taubyte/go-interfaces/services/substrate"
	countersIface "github.com/taubyte/go-interfaces/services/substrate/counters"
	databaseIface "github.com/taubyte/go-interfaces/services/substrate/database"
	httpIface "github.com/taubyte/go-interfaces/services/substrate/http"
	ipfsIface "github.com/taubyte/go-interfaces/services/substrate/ipfs"
	p2pIface "github.com/taubyte/go-interfaces/services/substrate/p2p"
	pubSubIface "github.com/taubyte/go-interfaces/services/substrate/pubsub"
	smartOpsIface "github.com/taubyte/go-interfaces/services/substrate/smartops"
	storageIface "github.com/taubyte/go-interfaces/services/substrate/storage"
	"github.com/taubyte/go-interfaces/services/tns"
	"github.com/taubyte/go-interfaces/vm"
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
	nodeCounters countersIface.Service
	nodeSmartOps smartOpsIface.Service

	tns tns.Client

	branch string

	orbitals []vm.Plugin
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

func (s *Service) Logger() logging.StandardLogger {
	return logger
}

func (s *Service) Counter() countersIface.Service {
	return s.nodeCounters
}

func (s *Service) SmartOps() smartOpsIface.Service {
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
