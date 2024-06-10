package peer

import (
	"context"
	"sync"

	ipfslite "github.com/hsanjuan/ipfs-lite"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	discovery "github.com/libp2p/go-libp2p/p2p/discovery/routing"
	netmock "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/taubyte/tau/p2p/datastores/mem"
)

var (
	mocknet     netmock.Mocknet
	mocknetLock sync.Mutex
)

func MockNode(ctx context.Context) Node {
	mocknetLock.Lock()
	if mocknet == nil {
		mocknet = netmock.New()
	}
	mocknetLock.Unlock()

	var (
		err error
		p   node
	)

	p.ctx, p.ctx_cancel = context.WithCancel(ctx)

	p.store = mem.New()

	p.host, err = mocknet.GenPeer()
	if err != nil {
		panic(err)
	}

	p.dht, err = dht.New(p.ctx, p.host)
	if err != nil {
		panic(err)
	}

	// Create ipfs node
	p.ipfs, err = ipfslite.New(p.ctx, p.store, nil, p.host, p.dht, nil)
	if err != nil {
		panic(err)
	}

	p.drouter = discovery.NewRoutingDiscovery(p.dht)

	// Prep messaging PUBSUB
	p.messaging, err = pubsub.NewGossipSub(
		p.ctx,
		p.host,
		pubsub.WithFloodPublish(true),
	)
	if err != nil {
		panic(err)
	}

	p.topics = make(map[string]*pubsub.Topic)

	return &p
}
