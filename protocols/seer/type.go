package seer

import (
	"database/sql"
	"regexp"
	"sync"

	"github.com/miekg/dns"
	"github.com/taubyte/go-interfaces/kvdb"
	iface "github.com/taubyte/go-interfaces/services/seer"
	"github.com/taubyte/p2p/peer"
	streams "github.com/taubyte/p2p/streams/service"

	tnsClient "github.com/taubyte/go-interfaces/services/tns"
	http "github.com/taubyte/http"
	"github.com/taubyte/odo/config"
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
	config.Protocol `yaml:"z,omitempty"`
}
