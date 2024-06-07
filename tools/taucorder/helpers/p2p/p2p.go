package p2p

import (
	"context"
	"fmt"

	corePeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/p2p/keypair"
	"github.com/taubyte/p2p/peer"
)

func New(ctx context.Context, nodes []corePeer.AddrInfo, swarmKey []byte) (peer.Node, error) {
	return peer.NewClientNode(
		ctx,
		nil,
		keypair.NewRaw(),
		swarmKey,
		[]string{fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", 11111)},
		nil,
		true,
		nodes,
	)
}
