package peer

import (
	"context"
	"fmt"
	"os"
	"sync"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
	discovery "github.com/libp2p/go-libp2p/p2p/discovery/routing"
	netmock "github.com/libp2p/go-libp2p/p2p/net/mock"

	helpers "github.com/taubyte/tau/p2p/helpers"
)

var (
	mocknet     netmock.Mocknet
	mocknetLock sync.Mutex
	// mocks are the mock nodes LinkAllPeers links until they close; the mocknet
	// itself keeps every peer it ever made.
	mocks = map[peer.ID]*node{}
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

	p.dag, err = newDAG(p.ctx, p.store, p.host, p.dht)
	if err != nil {
		panic(err)
	}

	p.drouter = discovery.NewRoutingDiscovery(p.dht)

	p.topics = make(map[string]*pubsub.Topic)

	p.messaging, err = pubsub.NewGossipSub(
		p.ctx,
		p.host,
		pubsub.WithFloodPublish(true),
	)
	if err != nil {
		panic(err)
	}

	err = p.dht.Bootstrap(p.ctx)
	if err != nil {
		logger.Warnf("mock DHT bootstrap failed: %v", err)
	}

	mocknetLock.Lock()
	mocks[p.id] = &p
	mocknetLock.Unlock()

	return &p
}

// LinkAllPeers links each pair of open mock nodes once and unlinks closed
// ones: the mocknet's LinkAll links every ordered pair of every peer it ever
// made on each call.
func LinkAllPeers() error {
	mocknetLock.Lock()
	defer mocknetLock.Unlock()
	if mocknet == nil {
		return fmt.Errorf("mocknet not initialized")
	}

	open := make([]peer.ID, 0, len(mocks))
	for id, n := range mocks {
		if n.ctx.Err() == nil {
			open = append(open, id)
			continue
		}
		delete(mocks, id)
		for _, other := range mocknet.Peers() {
			for _, l := range mocknet.LinksBetweenPeers(id, other) {
				if err := mocknet.Unlink(l); err != nil {
					return err
				}
			}
		}
	}

	for i, a := range open {
		for _, b := range open[i+1:] {
			if len(mocknet.LinksBetweenPeers(a, b)) > 0 {
				continue
			}
			if _, err := mocknet.LinkPeers(a, b); err != nil {
				return err
			}
		}
	}
	return nil
}
