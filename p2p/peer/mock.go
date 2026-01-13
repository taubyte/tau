package peer

import (
	"context"
	"fmt"
	"os"
	"sync"

	ipfslite "github.com/hsanjuan/ipfs-lite"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	discovery "github.com/libp2p/go-libp2p/p2p/discovery/routing"
	netmock "github.com/libp2p/go-libp2p/p2p/net/mock"

	helpers "github.com/taubyte/tau/p2p/helpers"
)

var (
	mocknet     netmock.Mocknet
	mocknetLock sync.Mutex
)

func Mock(ctx context.Context) Node {
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

	p.host, err = mocknet.GenPeer()
	if err != nil {
		panic(err)
	}

	p.id = p.host.ID()

	p.dht, err = dht.New(p.ctx, p.host)
	if err != nil {
		panic(err)
	}

	repoPath, err := os.MkdirTemp("", "tb-node-*")
	if err != nil {
		panic(err)
	}

	p.ephemeral_repo_path = true

	p.repo_path = fmt.Sprint(repoPath)

	p.store, err = helpers.NewDatastore(p.repo_path)
	if err != nil {
		panic(err)
	}

	// Create ipfs node
	p.ipfs, err = ipfslite.New(p.ctx, p.store, nil, p.host, p.dht, nil)
	if err != nil {
		panic(err)
	}

	p.drouter = discovery.NewRoutingDiscovery(p.dht)

	p.topics = make(map[string]*pubsub.Topic)

	// Prep messaging PUBSUB
	p.messaging, err = pubsub.NewGossipSub(
		p.ctx,
		p.host,
		pubsub.WithFloodPublish(true),
	)
	if err != nil {
		panic(err)
	}

	// Note: DHT bootstrap should be called after all nodes are linked and connected
	// Initial bootstrap here may fail if no peers are connected yet, which is fine
	// The bootstrap will be retried/refined after LinkAllPeers() is called
	err = p.dht.Bootstrap(p.ctx)
	if err != nil {
		// Expected error - no peers connected yet, will bootstrap after linking
		_ = err
	}

	return &p
}

// LinkAllPeers links all peers in the mocknet so they can communicate
// This should be called after creating all mock nodes that need to communicate
func LinkAllPeers() error {
	mocknetLock.Lock()
	defer mocknetLock.Unlock()
	if mocknet == nil {
		return fmt.Errorf("mocknet not initialized")
	}
	return mocknet.LinkAll()
}
