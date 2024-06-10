package peer

import (
	"context"
	"io"
	"sync"
	"time"

	crypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/pnet"
	"github.com/taubyte/utils/fs/dir"

	ipfslite "github.com/hsanjuan/ipfs-lite"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/config"
	"github.com/libp2p/go-libp2p/core/discovery"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"

	routing "github.com/libp2p/go-libp2p/core/routing"
)

type BootstrapParams struct {
	Enable bool
	Peers  []peer.AddrInfo
}

type Node interface {
	AddFile(r io.Reader) (string, error)
	AddFileForCid(r io.Reader) (cid.Cid, error)
	Close()
	Context() context.Context
	DAG() *ipfslite.Peer
	DeleteFile(id string) error
	Discovery() discovery.Discovery
	Done() <-chan struct{}
	GetFile(ctx context.Context, id string) (ReadSeekCloser, error)
	GetFileFromCid(ctx context.Context, cid cid.Cid) (ReadSeekCloser, error)
	ID() peer.ID
	Messaging() *pubsub.PubSub
	NewChildContextWithCancel() (context.Context, context.CancelFunc)
	NewFolder(name string) (dir.Directory, error)
	NewPubSubKeepAlive(ctx context.Context, cancel context.CancelFunc, name string) error
	Peer() host.Host
	Peering() PeeringService
	Ping(pid string, count int) (int, time.Duration, error)
	PubSubPublish(ctx context.Context, name string, data []byte) error
	PubSubSubscribe(name string, handler PubSubConsumerHandler, err_handler PubSubConsumerErrorHandler) error
	PubSubSubscribeContext(ctx context.Context, name string, handler PubSubConsumerHandler, err_handler PubSubConsumerErrorHandler) error
	PubSubSubscribeToTopic(topic *pubsub.Topic, handler PubSubConsumerHandler, err_handler PubSubConsumerErrorHandler) error
	SimpleAddrsFactory(announce []string, override bool) config.Option
	Store() datastore.Batching
	WaitForSwarm(timeout time.Duration) error
}

type node struct {
	ctx                 context.Context
	ctx_cancel          context.CancelFunc
	ephemeral_repo_path bool
	repo_path           string
	store               datastore.Batching
	key                 crypto.PrivKey
	id                  peer.ID
	secret              pnet.PSK
	host                host.Host
	dht                 routing.Routing
	drouter             discovery.Discovery
	messaging           *pubsub.PubSub
	ipfs                *ipfslite.Peer
	peering             PeeringService

	topicsMutex sync.Mutex
	topics      map[string]*pubsub.Topic
	closed      bool
}

func (p *node) ID() peer.ID {
	return p.id
}

func (p *node) Peering() PeeringService {
	return p.peering
}

func (p *node) Peer() host.Host {
	return p.host
}

func (p *node) Messaging() *(pubsub.PubSub) {
	return p.messaging
}

func (p *node) Store() datastore.Batching {
	return p.store
}

func (p *node) DAG() *ipfslite.Peer {
	return p.ipfs
}

func (p *node) Discovery() discovery.Discovery {
	return p.drouter
}

func (p *node) Context() context.Context {
	return p.ctx
}

type PeeringService interface {
	Start() error
	Stop() error
	AddPeer(peer.AddrInfo)
	RemovePeer(peer.ID)
}
