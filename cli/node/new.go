package node

import (
	"context"
	"fmt"

	"github.com/taubyte/odo/config"
	"github.com/taubyte/p2p/peer"
)

func NewNode(ctx context.Context, config *config.Protocol, databaseName string) (peer.Node, error) {
	if config.DevMode {
		return NewLiteNode(ctx, config, databaseName)
	}

	return nil, nil
}

func NewLiteNode(ctx context.Context, config *config.Protocol, databaseName string) (peer.Node, error) {
	bootstrapParam, err := convertBootstrap(config.Peers, config.DevMode)
	if err != nil {
		return nil, fmt.Errorf("getting bootstrap perms in NewLiteNode failed with: %s", err)
	}

	node, err := peer.NewLitePublic(
		ctx,
		config.Root+databaseName,
		config.PrivateKey,
		config.SwarmKey,
		config.P2PListen,
		config.P2PAnnounce,
		bootstrapParam,
	)
	if err != nil {
		return nil, err
	}

	err = node.WaitForSwarm(WaitForSwamDuration)
	if err != nil {
		return nil, err
	}

	return node, nil
}
