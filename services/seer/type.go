package seer

import (
	"time"

	"github.com/jellydator/ttlcache/v3"
	"github.com/miekg/dns"
	"github.com/taubyte/tau/core/kvdb"
	iface "github.com/taubyte/tau/core/services/seer"
	"github.com/taubyte/tau/p2p/peer"
	streams "github.com/taubyte/tau/p2p/streams/service"

	http "github.com/taubyte/http"
	"github.com/taubyte/tau/config"
	tnsClient "github.com/taubyte/tau/core/services/tns"

	"github.com/ipfs/go-datastore"
)

var (
	DefaultBlockTime         = 60 * time.Second
	ValidServiceResponseTime = 5 * time.Minute
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
	stream        *streams.CommandService
	geo           *geoService
	oracle        *oracleService
	dns           *dnsServer
	positiveCache *ttlcache.Cache[string, []string]
	negativeCache *ttlcache.Cache[string, bool]

	config *config.Node

	ds datastore.Batching

	tns         tnsClient.Client
	dnsResolver iface.Resolver

	hostUrl string

	shape   string
	odo     bool
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
