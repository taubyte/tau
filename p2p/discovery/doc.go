// Package discovery answers the one question p2p/rpc does not: which peers
// serve this protocol. rpc.NewClient dials a peer you have already named, so
// without this a service can reach "that auth node" and never "any auth
// node".
//
// One Discovery watches one protocol id and keeps the set of peers serving
// it. It hands out peer ids and nothing else — an id is all rpc.NewClient
// takes, and a package that handed back streams would own their lifetime too.
//
//	d, err := discovery.New(ctx, h, protocolID, discovery.WithRouting(node.Discovery()))
//	defer d.Close()
//
//	pid, err := d.Any(ctx)                         // or d.Peers(ctx, 4) to fan out
//	c, err := rpc.NewClient(h, pid, d.Protocol())  // the two lines that join the packages
//	auth := authv2connect.NewAuthServiceClient(c.HTTP(), c.URL())
//
// # Where the answers come from
//
// The peerstore is the first source and the one that always works: libp2p's
// identify tells every peer which protocols we handle, so a node that starts
// serving is visible to everyone it is connected to within a round trip, with
// nothing published anywhere. It is also the source that works under mocknet
// with no router in the picture at all, which is what lets a service test
// resolve a peer and then call it without a universe underneath.
//
// Routing — the DHT — is the second, and it is optional because a host.Host
// has no discovery of its own: pass WithRouting to get it. It is what finds a
// peer this node has never met, and it is the half that needs the serving node
// to have called Advertise. Both halves address the service by the same
// namespace, the protocol id itself, which is why Advertise lives here rather
// than beside rpc.Listen: the two strings have to be one string.
//
// That order — peerstore first, routing filling in behind it — is the design,
// and the reason is the clock routing runs on. tau does not ask the DHT
// directly: p2p/peer wraps it in libp2p's NewBackoffDiscovery, so a lookup
// that found nothing is answered from that same nothing for a minute
// (p2p/peer.DiscoveryBackoffMin) before the DHT is asked again. Correctly so —
// an empty routing table is empty for every caller, and re-asking it in a loop
// costs the swarm. But it means the routing half cannot be what a caller waits
// on, and a design that leaned on it would inherit that minute as its
// worst-case first call. Leaning on the peerstore instead is what removes the
// dependence: identify has already told this node what its neighbours serve.
// See DefaultRetryInterval for the one case the ticker is left holding.
//
// # Whether clients should be pooled, and where
//
// An rpc.Client is an HTTP/2 connection over one libp2p stream, and building
// one per call throws away the multiplexing that makes it worth having. So
// pooling is real, and it belongs in p2p/rpc, beside the constructor that
// knows how to build and close a client.
//
// It does not belong here. This package resolves; it does not connect, and a
// pool here would be keyed by peer within one protocol — so it could not share
// one connection between two protocols spoken to the same peer, and it would
// leave a caller that already knows the peer id (the "this specific node" case,
// which never asks discovery anything) with no pool at all. Nor does it belong
// in a service: eight of them would hold eight copies of the same map.
//
// Until rpc grows one, a caller that talks to a peer repeatedly should hold
// its rpc.Client rather than rebuild it, and Close it when done. Caching by
// peer id is safe: an id comes from a key pair and names that one node for
// good, so a peer that leaves and comes back is the same client.
//
// # Discovery does not dial
//
// Nothing here opens a connection. The addresses of a peer found through
// routing are put in the peerstore, and the caller's first RPC is what dials.
// That is deliberate: a discovery loop that dialled would compete with the
// caller for the same connection, and simultaneous dials in both directions
// are what put the swarm into dial backoff and starved discovery the last time
// this loop was investigated. Discovery reports; the caller connects; a caller
// whose connection fails says so with Failed.
package discovery
