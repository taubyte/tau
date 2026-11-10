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
// resolve a peer and then call it without a cloud underneath.
//
// What identify leaves in the peerstore can be older than what the peer last sent:
// it applies two messages from one peer at once and keeps whichever lands last, and
// the list never expires, so a peer that began to serve after its connection closed
// is still denied by what it said before. Nothing corrects either until the peer's
// protocols change while connected. So a peer the list leaves out, while it was a
// member or an identify message or routing named it, is asked: a stream on the
// protocol, which the host negotiates and records when the peer answers.
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
// It does not belong here. This package resolves, and a pool here would be
// keyed by peer within one protocol — so it could not share
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
// # When discovery dials
//
// Only to ask. The addresses of a peer found through routing are put in the
// peerstore, and a peer routing names with nothing known against it is handed out
// for the caller's first RPC to dial. A peer whose list denies what there is reason
// to believe is dialled when it is not connected, because a list nobody reconnects
// to is never corrected, and no caller connects to a peer it is never handed. Two
// nodes dialling each other at once both connect, since a node of the cloud dials
// from a port of its own, so the ask and a caller's dial do not cost each other the
// connection. A caller whose connection fails says so with Failed.
package discovery
