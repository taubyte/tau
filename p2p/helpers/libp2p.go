package helpers

import (
	"context"
	"sync"
	"time"

	ipns "github.com/ipfs/boxo/ipns"
	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/namespace"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p-kad-dht/dual"
	record "github.com/libp2p/go-libp2p-record"
	p2pConfig "github.com/libp2p/go-libp2p/config"
	crypto "github.com/libp2p/go-libp2p/core/crypto"
	host "github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	peer "github.com/libp2p/go-libp2p/core/peer"
	pnet "github.com/libp2p/go-libp2p/core/pnet"
	routing "github.com/libp2p/go-libp2p/core/routing"
	"github.com/libp2p/go-libp2p/p2p/host/autorelay"
	connmgr "github.com/libp2p/go-libp2p/p2p/net/connmgr"
	"github.com/libp2p/go-libp2p/p2p/protocol/identify"
	libp2ptls "github.com/libp2p/go-libp2p/p2p/security/tls"

	tcp "github.com/libp2p/go-libp2p/p2p/transport/tcp"
)

var (
	DefaultConnMgrHighWater   = 400
	DefaultConnMgrLowWater    = 100
	DefaultConnMgrGracePeriod = 2 * time.Minute
	DefaultDialPeerTimeout    = 3 * time.Second
)

const dhtNamespace = "dht"

func init() {
	// Cluster peers should advertise their public IPs as soon as they
	// learn about them. Default for this is 4, which prevents clusters
	// with less than 4 peers to advertise an external address they know
	// of, therefore they cannot be remembered by other peers asap. This
	// affects dockerized setups mostly. This may announce non-dialable
	// NATed addresses too eagerly, but they should progressively be
	// cleaned up.
	identify.ActivationThresh = 1
	network.DialPeerTimeout = 120 * time.Second
}

var Libp2pOptionsBase = []libp2p.Option{
	libp2p.Ping(true),
	libp2p.Security(libp2ptls.ID, libp2ptls.New),
	libp2p.NoTransports,
	libp2p.Transport(tcp.NewTCPTransport),
	libp2p.DefaultMuxers,
}

// Libp2pOptionsExtra provides some useful libp2p options
// to create a fully featured libp2p host. It can be used with
// SetupLibp2p.
var Libp2pOptionsFullNode = []libp2p.Option{
	libp2p.EnableNATService(),
	libp2p.EnableRelayService(),
	func(cfg *p2pConfig.Config) error {
		mgr, err := connmgr.NewConnManager(DefaultConnMgrLowWater*4, DefaultConnMgrHighWater*8, connmgr.WithGracePeriod(DefaultConnMgrGracePeriod))
		if err != nil {
			return err
		}

		return libp2p.ConnectionManager(mgr)(cfg)
	},
}

var Libp2pOptionsPublicNode = []libp2p.Option{
	libp2p.EnableNATService(),
	libp2p.EnableRelayService(),
	func(cfg *p2pConfig.Config) error {
		mgr, err := connmgr.NewConnManager(DefaultConnMgrLowWater*2, DefaultConnMgrHighWater*4, connmgr.WithGracePeriod(DefaultConnMgrGracePeriod))
		if err != nil {
			return err
		}

		return libp2p.ConnectionManager(mgr)(cfg)
	},
}

var Libp2pOptionsLitePublicNode = []libp2p.Option{
	func(cfg *p2pConfig.Config) error {
		mgr, err := connmgr.NewConnManager(DefaultConnMgrLowWater, DefaultConnMgrHighWater, connmgr.WithGracePeriod(DefaultConnMgrGracePeriod))
		if err != nil {
			return err
		}

		return libp2p.ConnectionManager(mgr)(cfg)
	},
}

var Libp2pSimpleNodeOptions = []libp2p.Option{
	func(cfg *p2pConfig.Config) error {
		mgr, err := connmgr.NewConnManager(DefaultConnMgrLowWater, DefaultConnMgrHighWater, connmgr.WithGracePeriod(DefaultConnMgrGracePeriod))
		if err != nil {
			return err
		}

		return libp2p.ConnectionManager(mgr)(cfg)
	},
}

var Libp2pLitePrivateNodeOptions = []libp2p.Option{
	func(cfg *p2pConfig.Config) error {
		mgr, err := connmgr.NewConnManager(DefaultConnMgrLowWater, DefaultConnMgrHighWater, connmgr.WithGracePeriod(DefaultConnMgrGracePeriod))
		if err != nil {
			return err
		}

		return libp2p.ConnectionManager(mgr)(cfg)
	},
	libp2p.EnableHolePunching(),
	libp2p.NATPortMap(),
}

// SetupLibp2p returns a routed host and DHT instances that can be used to
// easily create a ipfslite Peer. You may consider to use Peer.Bootstrap()
// after creating the IPFS-Lite Peer to connect to other peers. When the
// datastore parameter is nil, the DHT will use an in-memory datastore, so all
// provider records are lost on program shutdown.
//
// Additional libp2p options can be passed. Note that the Identity,
// ListenAddrs and PrivateNetwork options will be setup automatically.
// Interesting options to pass: NATPortMap() EnableAutoRelay(),
// libp2p.EnableNATService(), DisableRelay(), ConnectionManager(...)... see
// https://godoc.org/github.com/libp2p/go-libp2p#Option for more info.
//
// The secret should be a 32-byte pre-shared-key byte slice.
func SetupLibp2p(
	ctx context.Context,
	hostKey crypto.PrivKey,
	secret pnet.PSK,
	listenAddrs []string,
	ds datastore.Batching,
	bootstrapPeerFunc func() []peer.AddrInfo,
	opts ...libp2p.Option,
) (host.Host, routing.Routing, error) {

	var h host.Host
	var idht *dual.DHT
	var err error

	// a channel to wait until these variables have been set
	// (or left unset on errors). Mostly to avoid reading while writing.
	hostAndDHTReady := make(chan struct{})
	defer close(hostAndDHTReady)

	hostGetter := func() host.Host {
		<-hostAndDHTReady // closed when we finish NewClusterHost
		return h
	}

	dhtGetter := func() *dual.DHT {
		<-hostAndDHTReady // closed when we finish NewClusterHost
		return idht
	}

	opts = append([]libp2p.Option{
		libp2p.Identity(hostKey),
		libp2p.ListenAddrStrings(listenAddrs...),
		libp2p.PrivateNetwork(secret),
		libp2p.Routing(func(h host.Host) (routing.PeerRouting, error) {
			extraopts := make([]dual.Option, 0)
			if bootstrapPeerFunc != nil {
				extraopts = append(extraopts, dual.WanDHTOption(dht.BootstrapPeersFunc(bootstrapPeerFunc)))
			}
			idht, err = newDHT(ctx, h, ds, extraopts...)
			return idht, err
		}),
		libp2p.EnableAutoRelayWithPeerSource(newPeerSource(hostGetter, dhtGetter)),
	}, opts...)

	h, err = libp2p.New(opts...)
	if err != nil {
		return nil, nil, err
	}

	return h, idht, nil
}

func newDHT(ctx context.Context, h host.Host, store datastore.Batching, extraopts ...dual.Option) (*dual.DHT, error) {
	dhtDatastore := namespace.Wrap(store, datastore.NewKey(dhtNamespace))

	opts := []dual.Option{
		dual.DHTOption(dht.NamespacedValidator("pk", record.PublicKeyValidator{})),
		dual.DHTOption(dht.NamespacedValidator("ipns", ipns.Validator{KeyBook: h.Peerstore()})),
		dual.DHTOption(dht.Concurrency(10)),
		dual.DHTOption(dht.Mode(dht.ModeAuto)),
		dual.DHTOption(dht.Datastore(dhtDatastore)),
	}

	opts = append(opts, extraopts...)

	return dual.New(ctx, h, opts...)
}

// Inspired in Kubo's
// https://github.com/ipfs/go-ipfs/blob/9327ee64ce96ca6da29bb2a099e0e0930b0d9e09/core/node/libp2p/relay.go#L79-L103
// and https://github.com/ipfs/go-ipfs/blob/9327ee64ce96ca6da29bb2a099e0e0930b0d9e09/core/node/libp2p/routing.go#L242-L317
// but simplified and adapted:
//   - Everytime we need peers for relays we do a DHT lookup.
//   - We return the peers from that lookup.
//   - No need to do it async, since we have to wait for the full lookup to
//     return anyways. We put them on a buffered channel and be done.
func newPeerSource(hostGetter func() host.Host, dhtGetter func() *dual.DHT) autorelay.PeerSource {
	return func(ctx context.Context, numPeers int) <-chan peer.AddrInfo {
		// make a channel to return, and put items from numPeers on
		// that channel up to numPeers. Then close it.
		r := make(chan peer.AddrInfo, numPeers)
		defer close(r)

		// Because the Host, DHT are initialized after relay, we need to
		// obtain them indirectly this way.
		h := hostGetter()
		if h == nil { // context canceled etc.
			return r
		}
		idht := dhtGetter()
		if idht == nil { // context canceled etc.
			return r
		}

		// length of closest peers is K.
		closestPeers, err := idht.WAN.GetClosestPeers(ctx, h.ID().String())
		if err != nil { // Bail out. Usually a "no peers found".
			return r
		}

		for _, p := range closestPeers {
			addrs := h.Peerstore().Addrs(p)
			if len(addrs) == 0 {
				continue
			}
			dhtPeer := peer.AddrInfo{ID: p, Addrs: addrs}
			// Attempt to put peers on r if we have space,
			// otherwise return (we reached numPeers)
			select {
			case <-ctx.Done():
				return r
			case r <- dhtPeer:
			default:
				return r
			}
		}
		// We are here if numPeers > closestPeers
		return r
	}
}

// Bootstrap is an optional helper to connect to the given peers and bootstrap
// the Peer DHT (and Bitswap). This is a best-effort function. Errors are only
// logged and a warning is printed when less than half of the given peers
// could be contacted. It is fine to pass a list where some peers will not be
// reachable.
func Bootstrap(ctx context.Context, h host.Host, dht routing.Routing, peers []peer.AddrInfo) ([]peer.AddrInfo, error) {
	connected := make(chan struct{})

	var wg sync.WaitGroup
	for _, pinfo := range peers {
		wg.Add(1)
		go func(pinfo peer.AddrInfo) {
			defer wg.Done()
			err := h.Connect(ctx, pinfo)
			if err != nil {
				return
			}
			h.ConnManager().TagPeer(pinfo.ID, "bootstrap", 42)

			connected <- struct{}{}
		}(pinfo)
	}

	go func() {
		wg.Wait()
		close(connected)
	}()

	i := 0
	for range connected {
		i++
	}

	err := dht.Bootstrap(ctx)
	if err != nil {
		return peers, err
	}

	return peers, nil
}
