package discovery

import (
	"context"

	libdisc "github.com/libp2p/go-libp2p/core/discovery"
	"github.com/libp2p/go-libp2p/core/protocol"
	discoveryUtil "github.com/libp2p/go-libp2p/p2p/discovery/util"
)

// namespace is the name a service is published and looked up under: the protocol
// id, written once because a mismatch between the two halves fails silently.
func namespace(id protocol.ID) string { return string(id) }

// Advertise publishes this node as serving id, so that a peer which has never
// connected to it can still find it through routing. It returns immediately
// and republishes in the background until ctx ends.
//
// Connected peers already learn what it serves from identify, so forgetting this is
// invisible until the swarm is too large to be fully connected. It takes an
// Advertiser because a host.Host has no routing: pass the node discovery that
// WithRouting gets on the calling side.
func Advertise(ctx context.Context, a libdisc.Advertiser, id protocol.ID, opts ...libdisc.Option) {
	if a == nil || id == "" {
		return
	}
	discoveryUtil.Advertise(ctx, a, namespace(id), opts...)
}
