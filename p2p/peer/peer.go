package peer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	crypto "github.com/libp2p/go-libp2p/core/crypto"
	peer "github.com/libp2p/go-libp2p/core/peer"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	dirutils "github.com/taubyte/tau/utils/fs/dir"

	libp2ptls "github.com/libp2p/go-libp2p/p2p/security/tls"
	"github.com/taubyte/tau/p2p/protobook"
	"github.com/taubyte/tau/p2p/sectls"

	helpers "github.com/taubyte/tau/p2p/helpers"

	discoveryBackoff "github.com/libp2p/go-libp2p/p2p/discovery/backoff"
	discovery "github.com/libp2p/go-libp2p/p2p/discovery/routing"
)

// DiscoveryBackoffMin/Max bound how long a failed peer-discovery lookup is
// cached.
const (
	DiscoveryBackoffMin = 60 * time.Second
	DiscoveryBackoffMax = time.Hour
)

func DefaultBootstrapPeers() []peer.AddrInfo {
	peers, _ := peer.AddrInfosFromP2pAddrs(dht.DefaultBootstrapPeers...)
	return peers
}

func StandAlone() BootstrapParams {
	return BootstrapParams{Enable: false}
}

func Bootstrap(peers ...peer.AddrInfo) BootstrapParams {
	return BootstrapParams{Enable: true, Peers: peers}
}

func init() {
	// Bootstrappers are using 1024 keys. See:
	// https://github.com/ipfs/infra/issues/378
	crypto.MinRsaKeyBits = 1024
}

func (p *node) Close() {
	if p.closed.Swap(true) {
		return
	}

	if err := p.cleanup(); err != nil {
		logger.Errorf("cleanup failed: %v", err)
	}
}

// cleanup closes whatever the node opened, a half-built one included, and goes
// on past a failing step so nothing is left open.
func (p *node) cleanup() error {
	p.topicsMutex.Lock()
	defer p.topicsMutex.Unlock()

	p.topics = nil

	var errs []error
	if p.book != nil {
		p.book.Close()
	}
	if p.host != nil {
		if err := p.host.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing peer failed: %w", err))
		}
	}

	if p.peering != nil {
		if err := p.peering.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("stopping peering service failed: %w", err))
		}
	}
	if p.dht != nil {
		if closer, ok := p.dht.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				errs = append(errs, fmt.Errorf("closing DHT failed: %w", err))
			}
		}
	}

	// The context goes first, then the DAG service, before the store it reads.
	// Close is idempotent, so racing the autoclose is safe.
	p.ctx_cancel()
	if p.dag != nil {
		if err := p.dag.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing DAG service failed: %w", err))
		}
	}

	if p.store != nil {
		if err := p.store.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing datastore failed: %w", err))
		}
	}
	if p.ephemeral_repo_path {
		if err := os.RemoveAll(p.repo_path); err != nil {
			errs = append(errs, fmt.Errorf("removing temporary repository failed: %w", err))
		}
	}

	return errors.Join(errs...)
}

func (p *node) Done() <-chan struct{} {
	return p.ctx.Done()
}

func (p *node) NewFolder(name string) (dirutils.Directory, error) {
	return dirutils.New(fmt.Sprintf("%s/local/%s", p.repo_path, name))
}

func (p *node) WaitForSwarm(timeout time.Duration) error {
	wctx, wctx_c := context.WithTimeout(p.ctx, timeout)
	defer wctx_c()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if len(p.host.Peerstore().Peers()) > 0 {
				return nil
			}
		case <-wctx.Done():
			return fmt.Errorf("waiting for swarm timed out after %v: %w", timeout, wctx.Err())
		}
	}
}

func New(ctx context.Context, repoPath interface{}, privateKey []byte, admission *sectls.Credentials, swarmListen []string, swarmAnnounce []string, notPublic bool, bootstrap bool) (Node, error) {
	opts := make([]libp2p.Option, len(helpers.Libp2pSimpleNodeOptions))
	copy(opts, helpers.Libp2pSimpleNodeOptions)
	if notPublic {
		opts = append(opts, libp2p.ForceReachabilityPrivate(), libp2p.EnableRelay())
	}

	return new(ctx, repoPath, privateKey, admission, swarmListen, swarmAnnounce, BootstrapParams{Enable: bootstrap}, false, opts...)
}

func NewClientNode(ctx context.Context, repoPath interface{}, privateKey []byte, admission *sectls.Credentials, swarmListen []string, swarmAnnounce []string, notPublic bool, bootstrapers []peer.AddrInfo) (Node, error) {
	opts := make([]libp2p.Option, len(helpers.Libp2pLitePrivateNodeOptions))
	copy(opts, helpers.Libp2pLitePrivateNodeOptions)
	if notPublic {
		opts = append(opts, libp2p.ForceReachabilityPrivate(), libp2p.EnableRelay())
	}

	return new(ctx, repoPath, privateKey, admission, swarmListen, swarmAnnounce, BootstrapParams{Enable: true, Peers: bootstrapers}, false, opts...)
}

func NewWithBootstrapList(ctx context.Context, repoPath interface{}, privateKey []byte, admission *sectls.Credentials, swarmListen []string, swarmAnnounce []string, notPublic bool, bootstrapers []peer.AddrInfo) (Node, error) {
	opts := make([]libp2p.Option, len(helpers.Libp2pSimpleNodeOptions))
	copy(opts, helpers.Libp2pSimpleNodeOptions)
	if notPublic {
		opts = append(opts, libp2p.ForceReachabilityPrivate(), libp2p.EnableRelay())
	}

	return new(ctx, repoPath, privateKey, admission, swarmListen, swarmAnnounce, BootstrapParams{Enable: true, Peers: bootstrapers}, false, opts...)
}

func NewFull(ctx context.Context, repoPath interface{}, privateKey []byte, admission *sectls.Credentials, swarmListen []string, swarmAnnounce []string, isPublic bool, bootstrap BootstrapParams) (Node, error) {
	opts := make([]libp2p.Option, len(helpers.Libp2pOptionsFullNode))
	copy(opts, helpers.Libp2pOptionsFullNode)
	if isPublic {
		opts = append(opts, libp2p.ForceReachabilityPublic(), libp2p.EnableRelay())
	}

	return new(ctx, repoPath, privateKey, admission, swarmListen, swarmAnnounce, bootstrap, true, opts...)
}

func NewPublic(ctx context.Context, repoPath interface{}, privateKey []byte, admission *sectls.Credentials, swarmListen []string, swarmAnnounce []string, bootstrap BootstrapParams) (Node, error) {
	opts := make([]libp2p.Option, len(helpers.Libp2pOptionsPublicNode))
	copy(opts, helpers.Libp2pOptionsPublicNode)
	return new(ctx, repoPath, privateKey, admission, swarmListen, swarmAnnounce, bootstrap, true, opts...)
}

func NewLitePublic(ctx context.Context, repoPath interface{}, privateKey []byte, admission *sectls.Credentials, swarmListen []string, swarmAnnounce []string, bootstrap BootstrapParams) (Node, error) {
	opts := make([]libp2p.Option, len(helpers.Libp2pOptionsLitePublicNode))
	copy(opts, helpers.Libp2pOptionsLitePublicNode)
	return new(ctx, repoPath, privateKey, admission, swarmListen, swarmAnnounce, bootstrap, true, opts...)
}

func new(ctx context.Context, repoPath interface{}, privateKey []byte, admission *sectls.Credentials, swarmListen []string, swarmAnnounce []string, bootstrap BootstrapParams, server bool, opts ...libp2p.Option) (_ Node, err error) {
	var p node

	p.ctx, p.ctx_cancel = context.WithCancel(ctx)
	defer func() {
		if err != nil {
			err = errors.Join(err, p.cleanup())
		}
	}()

	p.ephemeral_repo_path = false
	if repoPath == nil {
		repoPath, err = os.MkdirTemp("", "tb-node-*")
		if err != nil {
			return nil, fmt.Errorf("creating temporary root failed with %w", err)
		}
		p.ephemeral_repo_path = true
	}

	p.repo_path = fmt.Sprint(repoPath)

	p.store, err = helpers.NewDatastore(p.repo_path)
	if err != nil {
		return nil, fmt.Errorf("creating datastore failed with %w", err)
	}

	p.key, err = crypto.UnmarshalPrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("processing private key failed with %w", err)
	}

	p.id, err = peer.IDFromPublicKey(p.key.GetPublic())
	if err != nil {
		return nil, fmt.Errorf("parsing pid failed with %w", err)
	}

	if opts == nil {
		opts = make([]libp2p.Option, 0)
	}

	opts = append(append([]libp2p.Option{security(admission)}, helpers.Libp2pOptionsBase...), opts...)

	opts = append(opts, libp2p.UserAgent(UserAgent))
	if server && swarmAnnounce != nil {
		opts = append(opts, p.SimpleAddrsFactory(swarmAnnounce, server))
	}

	// Gossipsub is up before the first connection: a peer learns this node speaks
	// it from identify on connecting, and one connected earlier hears it only from
	// a best-effort push it may miss. The DHT dials its bootstrap peers as soon as
	// it exists, so it gets none until then.
	var gossiping atomic.Bool
	bootstrapHandler := func() []peer.AddrInfo {
		if !gossiping.Load() {
			return nil
		}
		return bootstrap.Peers
	}

	p.host, p.dht, err = helpers.SetupLibp2p(
		p.ctx,
		p.key,
		swarmListen,
		p.store,
		bootstrapHandler,
		opts...,
	)
	if err != nil {
		return nil, fmt.Errorf("setting up libp2p failed: %w", err)
	}

	p.book, err = protobook.Keep(p.ctx, p.host)
	if err != nil {
		return nil, fmt.Errorf("keeping the protocol book failed: %w", err)
	}

	rng := rand.New(rand.NewSource(rand.Int63()))
	p.drouter, err = discoveryBackoff.NewBackoffDiscovery(
		discovery.NewRoutingDiscovery(p.dht),
		discoveryBackoff.NewExponentialBackoff(DiscoveryBackoffMin, DiscoveryBackoffMax, discoveryBackoff.FullJitter, time.Second, 5.0, 0, rng),
	)
	if err != nil {
		return nil, fmt.Errorf("creating backoff discovery failed: %w", err)
	}

	p.messaging, err = pubsub.NewGossipSub(
		p.ctx,
		p.host,
		pubsub.WithDiscovery(p.drouter),
		pubsub.WithFloodPublish(true),
		pubsub.WithMessageSigning(true),
		pubsub.WithStrictSignatureVerification(true),
	)
	if err != nil {
		return nil, fmt.Errorf("creating gossip pubsub failed: %w", err)
	}
	p.topics = make(map[string]*pubsub.Topic)
	gossiping.Store(true)

	p.dag, err = newDAG(p.ctx, p.store, p.host, p.dht)
	if err != nil {
		return nil, fmt.Errorf("creating DAG service failed: %w", err)
	}

	p.peering = NewPeeringService(&p)
	err = p.peering.Start()
	if err != nil {
		return nil, fmt.Errorf("starting peering service failed: %w", err)
	}

	if bootstrap.Enable {
		bnodes, err := helpers.Bootstrap(p.ctx, p.host, p.dht, bootstrap.Peers)
		if err != nil {
			return nil, fmt.Errorf("bootstrap failed: %w", err)
		}

		for _, n := range bnodes {
			p.peering.AddPeer(n)
		}
	} else {
		err := p.dht.Bootstrap(p.ctx)
		if err != nil {
			p.ctx_cancel()
			return nil, fmt.Errorf("DHT bootstrap failed: %w", err)
		}
	}

	return &p, nil
}

// Only peers with a certificate from the cloud's CA are admitted; a node
// without credentials is open and speaks stock libp2p TLS.
func security(admission *sectls.Credentials) libp2p.Option {
	if admission == nil {
		return libp2p.Security(libp2ptls.ID, libp2ptls.New)
	}
	return libp2p.Security(sectls.ID, sectls.New(admission))
}
