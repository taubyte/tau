package hoarder

import (
	"context"
	"fmt"
	"sync"

	"github.com/ipfs/go-datastore"
	hoarderIface "github.com/taubyte/go-interfaces/services/hoarder"
	ifaceTns "github.com/taubyte/go-interfaces/services/tns"
	"github.com/taubyte/p2p/peer"
	streams "github.com/taubyte/p2p/streams/service"
	"github.com/taubyte/tau/config"
)

var _ hoarderIface.Service = &Service{}

type Service struct {
	ctx            context.Context
	node           peer.Node
	tnsClient      ifaceTns.Client
	store          datastore.Batching
	stream         *streams.CommandService
	regLock        sync.RWMutex
	auctions       auctionStore
	auctionHistory auctionHistory
	lotteryPool    lotteryPool
}

func (s *Service) Node() peer.Node {
	return s.node
}

func (s *Service) Datastore() datastore.Batching {
	return s.store
}

type Config struct {
	config.Protocol `yaml:"z,omitempty"`
}

func (c *Config) String() string {
	return fmt.Sprintf("Hoarder config:\n\tRoot:%s\n", c.Root)
}

type registryItem struct {
	Replicas int
}

type lotteryPool map[string][]*hoarderIface.Auction
type auctionStore map[string]*hoarderIface.Auction
type auctionHistory map[string]map[string][]hoarderIface.AuctionType
