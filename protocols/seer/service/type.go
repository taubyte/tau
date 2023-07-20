package service

import (
	"database/sql"
	"regexp"
	"sync"

	streams "bitbucket.org/taubyte/p2p/streams/service"
	"github.com/miekg/dns"
	"github.com/taubyte/go-interfaces/kvdb"
	peer "github.com/taubyte/go-interfaces/p2p/peer"
	iface "github.com/taubyte/go-interfaces/services/seer"

	commonIface "github.com/taubyte/go-interfaces/services/common"
	"github.com/taubyte/go-interfaces/services/http"
	tnsClient "github.com/taubyte/go-interfaces/services/tns"
)

type Data map[string]interface{}
type dnsServer struct {
	Tcp  *dns.Server
	Udp  *dns.Server
	Seer *Service
}

type nodeData struct {
	Cid      string
	Services iface.Services
}

type oracleService struct {
	seer *Service
}

var _ iface.Service = &Service{}

type geoService struct {
	seer *Service
}

type Service struct {
	node   peer.Node
	db     kvdb.KVDB
	http   http.Service
	stream *streams.CommandService
	geo    *geoService
	oracle *oracleService
	dns    *dnsServer

	nodeDBMutex sync.RWMutex
	nodeDB      *sql.DB

	tns         tnsClient.Client
	dnsResolver iface.Resolver

	hostUrl string

	generatedDomain string
	caaRecordBypass *regexp.Regexp // TOOD: move this into go-specs
	shape           string
	odo             bool
}

func (s *Service) Node() peer.Node {
	return s.node
}

func (s *Service) KV() kvdb.KVDB {
	return s.db
}

func (s *Service) Resolver() iface.Resolver {
	return s.dnsResolver
}

type Config struct {
	commonIface.GenericConfig `yaml:"z,omitempty"`
}
