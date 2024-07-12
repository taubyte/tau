package ipfs

import (
	"context"
	"errors"
	"fmt"

	ipfsLite "github.com/hsanjuan/ipfs-lite"
	"github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/p2p/peer"
)

func New(ctx context.Context, options ...Option) (*Service, error) {
	var err error

	node := &Service{}

	for _, opts := range options {
		if err = opts(node); err != nil {
			return nil, fmt.Errorf("failed running option with error: %w", err)
		}
	}

	if len(node.swarmListen) == 0 {
		return nil, errors.New("swarm Listen cannot be empty")
	}

	if len(node.privateKey) == 0 {
		node.privateKey = keypair.NewRaw()
	}

	node.Node, err = peer.NewWithBootstrapList(ctx, nil, node.privateKey, nil, node.swarmListen, node.swarmAnnounce, node.private, ipfsLite.DefaultBootstrapPeers())
	if err != nil {
		return nil, fmt.Errorf("failed creating new node with error: %v", err)
	}

	return node, nil
}
