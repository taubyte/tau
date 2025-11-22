package seer

import (
	"time"

	"github.com/jellydator/ttlcache/v3"
	"github.com/miekg/dns"
	"github.com/taubyte/tau/core/kvdb"
	iface "github.com/taubyte/tau/core/services/seer"
	"github.com/taubyte/tau/p2p/peer"
	streams "github.com/taubyte/tau/p2p/streams/service"

	"github.com/taubyte/tau/config"
	tnsClient "github.com/taubyte/tau/core/services/tns"
	http "github.com/taubyte/tau/pkg/http"
	"github.com/taubyte/tau/pkg/poe"

	"github.com/ipfs/go-datastore"
)

var (
	MaxDnsResponseTime       = 3 * time.Second
	ServerIpCacheTTL         = 30 * time.Second
	PositiveCacheTTL         = 1 * time.Minute //5 * time.Minute
	DefaultBlockTime         = 1 * time.Minute
	ValidServiceResponseTime = 1 * time.Minute // 5 * time.Minute
)

type dnsServer struct {
	Tcp  *dns.Server
	Udp  *dns.Server
	Seer *Service
}

type nodeData struct {
	Cid string `cbor:"1,keyasint,omitempty"`

	Hostname string `cbor:"2,keyasint"`

	Services *iface.Services     `cbor:"8,keyasint,omitempty"`
	Usage    *iface.UsageData    `cbor:"9,keyasint,omitempty"`
	Geo      *iface.PeerLocation `cbor:"10,keyasint,omitempty"`
}

type oracleService struct {
	*Service
}

type geoService struct {
	*Service
}

type Service struct {
	node          peer.Node
	http          http.Service
	stream        streams.CommandService
	geo           *geoService
	oracle        *oracleService
	dns           *dnsServer
	positiveCache *ttlcache.Cache[string, []string]
	negativeCache *ttlcache.Cache[string, bool]

	config *config.Node

	ds datastore.Batching

	tns         tnsClient.Client
	dnsResolver iface.Resolver

	poe poe.Engine

	hostUrl string

	shape   string
	devMode bool
}

func (s *Service) Node() peer.Node {
	return s.node
}

func (s *Service) KV() kvdb.KVDB {
	return nil
}

func (s *Service) Resolver() iface.Resolver {
	return s.dnsResolver
}

type Config struct {
	config.Node `yaml:"z,omitempty"`
}
