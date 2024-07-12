package service

import (
	http "github.com/taubyte/http"
	iface "github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/p2p/peer"
	streams "github.com/taubyte/tau/p2p/streams/service"

	auth "github.com/taubyte/tau/core/services/auth"

	"github.com/taubyte/tau/config"
	monkey "github.com/taubyte/tau/core/services/monkey"
	tns "github.com/taubyte/tau/core/services/tns"

	libp2p "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/core/kvdb"
)

var _ iface.Service = &PatrickService{}

type PatrickService struct {
	monkeyClient monkey.Client
	node         peer.Node
	http         http.Service
	stream       *streams.CommandService
	authClient   auth.Client
	tnsClient    tns.Client
	db           kvdb.KVDB
	dbFactory    kvdb.Factory
	devMode      bool

	hostUrl string
}

func (s *PatrickService) KV() kvdb.KVDB {
	return s.db
}

func (s *PatrickService) Node() peer.Node {
	return s.node
}

type Config struct {
	config.Node `yaml:"z,omitempty"`
}

// TODO: optimize cbor storage
type Lock struct {
	Pid       libp2p.ID `cbor:"4,keyasint"`
	Timestamp int64     `cbor:"8,keyasint"`
	Eta       int64     `cbor:"16,keyasint"`
}
