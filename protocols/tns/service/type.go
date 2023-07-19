package service

import (
	"github.com/taubyte/go-interfaces/kvdb"
	commonIface "github.com/taubyte/go-interfaces/services/common"
	iface "github.com/taubyte/go-interfaces/services/tns"
	"github.com/taubyte/odo/protocols/tns/engine"
	"github.com/taubyte/p2p/peer"
	streams "github.com/taubyte/p2p/streams/service"
)

var _ iface.Service = &Service{}

type Service struct {
	node   *peer.Node
	db     kvdb.KVDB
	stream *streams.CommandService
	engine *engine.Engine
}

func (s *Service) Node() *peer.Node {
	return s.node
}

func (s *Service) KV() kvdb.KVDB {
	return s.db
}

type Config struct {
	commonIface.GenericConfig `yaml:"z,omitempty"`
}
