package tns

import (
	"sync"

	"github.com/taubyte/tau/core/kvdb"
	iface "github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/p2p/peer"
	streams "github.com/taubyte/tau/p2p/streams/service"
	"github.com/taubyte/tau/services/tns/engine"
)

var _ iface.Service = &Service{}

type Service struct {
	node      peer.Node
	db        kvdb.KVDB
	dbFactory kvdb.Factory
	stream    streams.CommandService
	engine    *engine.Engine

	pendingLock sync.Mutex
	pending     map[string]bool
}

func (s *Service) Node() peer.Node {
	return s.node
}

func (s *Service) KV() kvdb.KVDB {
	return s.db
}
