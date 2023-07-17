package service

import (
	"context"

	kv "github.com/taubyte/go-interfaces/kvdb"
	peer "github.com/taubyte/go-interfaces/p2p/peer"

	streams "bitbucket.org/taubyte/p2p/streams/service"
	iface "github.com/taubyte/go-interfaces/services/billing"
	commonIface "github.com/taubyte/go-interfaces/services/common"

	"github.com/taubyte/go-interfaces/services/http"

	authApi "bitbucket.org/taubyte/auth/api/p2p"
)

var _ iface.Service = &BillingService{}

type customersService struct {
	billing *BillingService
}

type customer struct {
	parent *customersService
	id     string
}

type BillingService struct {
	ctx        context.Context
	node       peer.Node
	db         kv.KVDB
	http       http.Service
	stream     *streams.CommandService
	customers  *customersService
	authClient *authApi.Client
	sandbox    bool
}

func (s *BillingService) Node() peer.Node {
	return s.node
}

func (s *BillingService) KV() kv.KVDB {
	return s.db
}

type Config struct {
	commonIface.GenericConfig `yaml:"z,omitempty"`
}

type counter struct {
	key   string
	value interface{}
}
