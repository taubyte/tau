package service

import (
	iface "github.com/taubyte/go-interfaces/services/patrick"
	http "github.com/taubyte/http"
	"github.com/taubyte/p2p/peer"
	streams "github.com/taubyte/p2p/streams/service"

	ifaceTns "github.com/taubyte/go-interfaces/services/tns"
	auth "github.com/taubyte/tau/clients/p2p/auth"
	monkey "github.com/taubyte/tau/clients/p2p/monkey"
	"github.com/taubyte/tau/config"

	libp2p "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/go-interfaces/kvdb"
)

var _ iface.Service = &PatrickService{}

type PatrickService struct {
	monkeyClient *monkey.Client
	node         peer.Node
	http         http.Service
	stream       *streams.CommandService
	authClient   *auth.Client
	tnsClient    ifaceTns.Client
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
	config.Protocol `yaml:"z,omitempty"`
}

// TODO: optimize cbor storage
type Lock struct {
	Pid       libp2p.ID `cbor:"4,keyasint"`
	Timestamp int64     `cbor:"8,keyasint"`
	Eta       int64     `cbor:"16,keyasint"`
}
