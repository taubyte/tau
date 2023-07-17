package service

import (
	"context"
	"fmt"
	"sync"

	streams "bitbucket.org/taubyte/p2p/streams/service"
	"github.com/ipfs/go-datastore"
	peer "github.com/taubyte/go-interfaces/p2p/peer"
	commonIface "github.com/taubyte/go-interfaces/services/common"
	hoarderIface "github.com/taubyte/go-interfaces/services/hoarder"
	ifaceTns "github.com/taubyte/go-interfaces/services/tns"
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
	commonIface.GenericConfig `yaml:"z,omitempty"`
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
