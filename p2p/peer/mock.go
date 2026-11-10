package peer

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	discovery "github.com/libp2p/go-libp2p/p2p/discovery/routing"
	netmock "github.com/libp2p/go-libp2p/p2p/net/mock"
	ma "github.com/multiformats/go-multiaddr"

	helpers "github.com/taubyte/tau/p2p/helpers"
	"github.com/taubyte/tau/p2p/protobook"
)

// Mocknet is an in-memory network of mock nodes. Only the nodes made on one can
// reach each other, so a test makes its own.
type Mocknet struct {
	mu  sync.Mutex
	net netmock.Mocknet
	// nodes are the ones LinkAll links until they close; the mocknet itself keeps
	// every peer it ever made.
	nodes map[peer.ID]*node
}

// NewMocknet is an empty network.
func NewMocknet() *Mocknet {
	return &Mocknet{net: netmock.New(), nodes: map[peer.ID]*node{}}
}

// Close takes the network down, with every host on it.
func (m *Mocknet) Close() error { return m.net.Close() }

// host is a mocknet peer keyed like a real node. mocknet's GenPeer signs with
// ECDSA where a node signs with Ed25519, and every pubsub message is signed and then
// checked by its own sender: a mock kvdb spent half of each write on ECDSA.
func (m *Mocknet) host() (host.Host, error) {
	sk, _, err := crypto.GenerateKeyPair(crypto.Ed25519, -1)
	if err != nil {
		return nil, err
	}
	id, err := peer.IDFromPrivateKey(sk)
	if err != nil {
		return nil, err
	}
	// An address in the blackhole range mocknet gives its own peers, unique by the
	// id's tail.
	ip := net.ParseIP("100::")
	suffix := []byte(id)
	if len(suffix) > 8 {
		suffix = suffix[len(suffix)-8:]
	}
	copy(ip[net.IPv6len-len(suffix):], suffix)
	addr, err := ma.NewMultiaddr(fmt.Sprintf("/ip6/%s/tcp/4242", ip))
	if err != nil {
		return nil, err
	}
	return m.net.AddPeer(sk, addr)
}

// Mock is a node on m that lives until ctx ends or it is closed.
func (m *Mocknet) Mock(ctx context.Context) Node {
	var (
		err error
		p   node
	)

	p.ctx, p.ctx_cancel = context.WithCancel(ctx)

	p.host, err = m.host()
	if err != nil {
		panic(err)
	}

	p.book, err = protobook.Keep(p.ctx, p.host)
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

	m.mu.Lock()
	m.nodes[p.id] = &p
	m.mu.Unlock()

	return &p
}

// LinkAll links each pair of m's open nodes once and unlinks closed ones: the
// mocknet's own LinkAll links every ordered pair of every peer it ever made on
// each call.
func (m *Mocknet) LinkAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	open := make([]peer.ID, 0, len(m.nodes))
	for id, n := range m.nodes {
		if n.ctx.Err() == nil {
			open = append(open, id)
			continue
		}
		delete(m.nodes, id)
		for _, other := range m.net.Peers() {
			for _, l := range m.net.LinksBetweenPeers(id, other) {
				if err := m.net.Unlink(l); err != nil {
					return err
				}
			}
		}
	}

	for i, a := range open {
		for _, b := range open[i+1:] {
			if len(m.net.LinksBetweenPeers(a, b)) > 0 {
				continue
			}
			if _, err := m.net.LinkPeers(a, b); err != nil {
				return err
			}
		}
	}
	return nil
}
