package peer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p-kad-dht/dual"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	crypto "github.com/libp2p/go-libp2p/core/crypto"
	peer "github.com/libp2p/go-libp2p/core/peer"

	ipfslite "github.com/hsanjuan/ipfs-lite"
	dirutils "github.com/taubyte/utils/fs/dir"

	"github.com/libp2p/go-libp2p/core/pnet"

	helpers "github.com/taubyte/tau/p2p/helpers"

	discoveryBackoff "github.com/libp2p/go-libp2p/p2p/discovery/backoff"
	discovery "github.com/libp2p/go-libp2p/p2p/discovery/routing"
)

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
	err := p.cleanup()
	if err != nil {
		panic(err)
	}

	p.closed = true
}

func (p *node) cleanup() error {
	p.topicsMutex.Lock()
	defer p.topicsMutex.Unlock()

	p.topics = nil

	if err := p.Peer().Close(); err != nil {
		return err
	}

	if p.peering != nil {
		if err := p.peering.Stop(); err != nil {
			return err
		}
	}
	if p.dht != nil {
		// Need to determine the type of DHT then close it
		switch p.dht.(type) {
		case *dht.IpfsDHT:
			if err := p.dht.(*dht.IpfsDHT).Close(); err != nil {
				return err
			}
		case *dual.DHT:
			if err := p.dht.(*dual.DHT).Close(); err != nil {
				return err
			}
		}
	}

	if p.store != nil {
		if err := p.store.Close(); err != nil {
			return err
		}
	}
	if p.ephemeral_repo_path {
		os.RemoveAll(p.repo_path)
	}

	p.ctx_cancel()

	return nil
}

func (p *node) Done() <-chan struct{} {
	return p.ctx.Done()
}

// Create a folder inside node root folder
func (p *node) NewFolder(name string) (dirutils.Directory, error) {
	return dirutils.New(fmt.Sprintf("%s/local/%s", p.repo_path, name))
}

func (p *node) WaitForSwarm(timeout time.Duration) error {
	wctx, wctx_c := context.WithTimeout(p.ctx, timeout)
	defer wctx_c()
	for {
		select {
		case <-time.After(100 * time.Millisecond):
			if len(p.host.Peerstore().Peers()) > 0 {
				return nil
			}
		case <-wctx.Done():
			return errors.New("not able to connect to other peers")
		}

	}
}

func New(ctx context.Context, repoPath interface{}, privateKey []byte, swarmKey []byte, swarmListen []string, swarmAnnounce []string, notPublic bool, bootstrap bool) (Node, error) {
	opts := make([]libp2p.Option, len(helpers.Libp2pSimpleNodeOptions))
	copy(opts, helpers.Libp2pSimpleNodeOptions)
	if notPublic {
		opts = append(opts, libp2p.ForceReachabilityPrivate(), libp2p.EnableRelay())
	}

	return new(ctx, repoPath, privateKey, swarmKey, swarmListen, swarmAnnounce, BootstrapParams{Enable: bootstrap}, false, opts...)
}

func NewClientNode(ctx context.Context, repoPath interface{}, privateKey []byte, swarmKey []byte, swarmListen []string, swarmAnnounce []string, notPublic bool, bootstrapers []peer.AddrInfo) (Node, error) {
	opts := make([]libp2p.Option, len(helpers.Libp2pLitePrivateNodeOptions))
	copy(opts, helpers.Libp2pLitePrivateNodeOptions)
	if notPublic {
		opts = append(opts, libp2p.ForceReachabilityPrivate(), libp2p.EnableRelay())
	}

	return new(ctx, repoPath, privateKey, swarmKey, swarmListen, swarmAnnounce, BootstrapParams{Enable: true, Peers: bootstrapers}, false, opts...)
}

func NewWithBootstrapList(ctx context.Context, repoPath interface{}, privateKey []byte, swarmKey []byte, swarmListen []string, swarmAnnounce []string, notPublic bool, bootstrapers []peer.AddrInfo) (Node, error) {
	opts := make([]libp2p.Option, len(helpers.Libp2pSimpleNodeOptions))
	copy(opts, helpers.Libp2pSimpleNodeOptions)
	if notPublic {
		opts = append(opts, libp2p.ForceReachabilityPrivate(), libp2p.EnableRelay())
	}

	return new(ctx, repoPath, privateKey, swarmKey, swarmListen, swarmAnnounce, BootstrapParams{Enable: true, Peers: bootstrapers}, false, opts...)
}

func NewFull(ctx context.Context, repoPath interface{}, privateKey []byte, swarmKey []byte, swarmListen []string, swarmAnnounce []string, isPublic bool, bootstrap BootstrapParams) (Node, error) {
	opts := make([]libp2p.Option, len(helpers.Libp2pOptionsFullNode))
	copy(opts, helpers.Libp2pOptionsFullNode)
	if isPublic {
		opts = append(opts, libp2p.ForceReachabilityPublic(), libp2p.EnableRelay())
	}

	return new(ctx, repoPath, privateKey, swarmKey, swarmListen, swarmAnnounce, bootstrap, true, opts...)
}

func NewPublic(ctx context.Context, repoPath interface{}, privateKey []byte, swarmKey []byte, swarmListen []string, swarmAnnounce []string, bootstrap BootstrapParams) (Node, error) {
	opts := make([]libp2p.Option, len(helpers.Libp2pOptionsPublicNode))
	copy(opts, helpers.Libp2pOptionsPublicNode)
	return new(ctx, repoPath, privateKey, swarmKey, swarmListen, swarmAnnounce, bootstrap, true, opts...)
}

func NewLitePublic(ctx context.Context, repoPath interface{}, privateKey []byte, swarmKey []byte, swarmListen []string, swarmAnnounce []string, bootstrap BootstrapParams) (Node, error) {
	opts := make([]libp2p.Option, len(helpers.Libp2pOptionsLitePublicNode))
	copy(opts, helpers.Libp2pOptionsLitePublicNode)
	return new(ctx, repoPath, privateKey, swarmKey, swarmListen, swarmAnnounce, bootstrap, true, opts...)
}

func new(ctx context.Context, repoPath interface{}, privateKey []byte, swarmKey []byte, swarmListen []string, swarmAnnounce []string, bootstrap BootstrapParams, server bool, opts ...libp2p.Option) (Node, error) {
	var p node
	var err error

	p.ctx, p.ctx_cancel = context.WithCancel(ctx)

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

	// Read key
	p.key, err = crypto.UnmarshalPrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("processing private key failed with %w", err)
	}

	// Generate ID
	p.id, err = peer.IDFromPublicKey(p.key.GetPublic())
	if err != nil {
		return nil, fmt.Errorf("parsing pid failed with %w", err)
	}

	// Read swarm key
	if swarmKey != nil {
		p.secret, err = pnet.DecodeV1PSK(bytes.NewReader(swarmKey))
		if err != nil {
			return nil, fmt.Errorf("reading swarm key failed with %w", err)
		}
	}

	// https://github.com/libp2p/go-libp2p/blob/d4d6adff6e3260792cb4514c27368059f2558530/options.go
	if opts == nil {
		opts = make([]libp2p.Option, 0)
	}

	opts = append(helpers.Libp2pOptionsBase, opts...)

	opts = append(opts, libp2p.UserAgent(UserAgent))
	if server && swarmAnnounce != nil {
		opts = append(opts, p.SimpleAddrsFactory(swarmAnnounce, server))
	}

	bootstrapHandler := func() []peer.AddrInfo {
		return bootstrap.Peers
	}

	p.host, p.dht, err = helpers.SetupLibp2p(
		p.ctx,
		p.key,
		p.secret,
		swarmListen,
		p.store,
		bootstrapHandler,
		opts...,
	)
	if err != nil {
		return nil, err
	}

	// Create ipfs node
	p.ipfs, err = ipfslite.New(p.ctx, p.store, nil, p.host, p.dht, nil)
	if err != nil {
		return nil, err
	}

	p.peering = NewPeeringService(&p)
	err = p.peering.Start()
	if err != nil {
		return nil, err
	}

	if bootstrap.Enable {
		// Bootstrap
		bnodes, err := helpers.Bootstrap(p.ctx, p.host, p.dht, bootstrap.Peers)
		if err != nil {
			return nil, err
		}

		// TODO: get the peering service out of bootsrap
		for _, n := range bnodes {
			p.peering.AddPeer(n)
		}
	} else {
		err := p.dht.Bootstrap(p.ctx)
		if err != nil {
			p.ctx_cancel()
			return nil, err
		}
	}

	// Prep Discoverer
	minBackoff, maxBackoff := time.Second*60, time.Hour
	rng := rand.New(rand.NewSource(rand.Int63()))
	p.drouter, err = discoveryBackoff.NewBackoffDiscovery(
		discovery.NewRoutingDiscovery(p.dht),
		discoveryBackoff.NewExponentialBackoff(minBackoff, maxBackoff, discoveryBackoff.FullJitter, time.Second, 5.0, 0, rng),
	)
	if err != nil {
		return nil, err
	}

	// Prep messaging PUBSUB
	p.messaging, err = pubsub.NewGossipSub(
		p.ctx,
		p.host,
		pubsub.WithDiscovery(p.drouter),
		pubsub.WithFloodPublish(true),
		pubsub.WithMessageSigning(true),
		pubsub.WithStrictSignatureVerification(true),
	)
	if err != nil {
		return nil, err
	}

	p.topics = make(map[string]*pubsub.Topic)
	return &p, nil
}
