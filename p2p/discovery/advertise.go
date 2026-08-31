package discovery

import (
	"context"

	libdisc "github.com/libp2p/go-libp2p/core/discovery"
	"github.com/libp2p/go-libp2p/core/protocol"
	discoveryUtil "github.com/libp2p/go-libp2p/p2p/discovery/util"
)

// namespace is the name a service is published and looked up under. It is the
// protocol id itself, and it is written once because the two halves are
// useless unless they are the same string: a serving node advertising one name
// while its callers search another is a service nobody outside its own
// peerstore can find, and neither side errors.
func namespace(id protocol.ID) string { return string(id) }

// Advertise publishes this node as serving id, so that a peer which has never
// connected to it can still find it through routing. It returns immediately
// and republishes in the background until ctx ends.
//
// A node only needs this for the routing half. Peers already connected to it
// learn what it serves from identify, with nothing published anywhere, which
// is why a swarm small enough to be fully connected works without ever calling
// this — and why forgetting to call it is invisible until the swarm is large.
//
// It takes an Advertiser rather than a host because a host.Host has no
// routing: pass the same node discovery that WithRouting gets on the calling
// side.
func Advertise(ctx context.Context, a libdisc.Advertiser, id protocol.ID, opts ...libdisc.Option) {
	if a == nil || id == "" {
		return
	}
	discoveryUtil.Advertise(ctx, a, namespace(id), opts...)
}
