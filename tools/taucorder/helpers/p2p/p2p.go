package p2p

import (
	"context"

	corePeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/p2p/peer"
)

func New(ctx context.Context, nodes []corePeer.AddrInfo, swarmKey []byte) (peer.Node, error) {
	return peer.NewClientNode(
		ctx,
		nil,
		keypair.NewRaw(),
		swarmKey,
		nil,
		nil,
		true,
		nodes,
	)
}
