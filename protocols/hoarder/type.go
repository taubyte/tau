package hoarder

import (
	"sync"

	"github.com/taubyte/go-interfaces/kvdb"
	hoarderIface "github.com/taubyte/go-interfaces/services/hoarder"
	ifaceTns "github.com/taubyte/go-interfaces/services/tns"
	"github.com/taubyte/p2p/peer"
	streams "github.com/taubyte/p2p/streams/service"
)

var _ hoarderIface.Service = &Service{}

type Service struct {
	node           peer.Node
	tnsClient      ifaceTns.Client
	db             kvdb.KVDB
	dbFactory      kvdb.Factory
	stream         *streams.CommandService
	regLock        sync.RWMutex
	auctions       auctionStore
	auctionHistory auctionHistory
	lotteryPool    lotteryPool
}

func (s *Service) Node() peer.Node {
	return s.node
}

type registryItem struct {
	Replicas int
}

type lotteryPool map[string][]*hoarderIface.Auction
type auctionStore map[string]*hoarderIface.Auction
type auctionHistory map[string]map[string][]hoarderIface.AuctionType
