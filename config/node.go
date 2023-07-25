package config

import (
	"context"
	"fmt"
	"path"
	"time"

	"github.com/taubyte/odo/utils"
	"github.com/taubyte/p2p/peer"
)

var WaitForSwamDuration = 10 * time.Second

func NewNode(ctx context.Context, config *Protocol, databaseName string) (peer.Node, error) {
	if config.DevMode {
		return NewLiteNode(ctx, config, databaseName)
	}

	bootstrapParam, err := utils.ConvertBootstrap(config.Peers, config.DevMode)
	if err != nil {
		return nil, fmt.Errorf("getting bootstrap perms in NewNode failed with: %s", err)
	}

	peerNode, err := peer.NewPublic(
		ctx,
		path.Join(config.Root, databaseName),
		config.PrivateKey,
		config.SwarmKey,
		config.P2PListen,
		config.P2PAnnounce,
		bootstrapParam,
	)
	if err != nil {
		return nil, err
	}

	err = peerNode.WaitForSwarm(WaitForSwamDuration)
	if err != nil {
		return nil, err
	}

	return peerNode, nil
}

func NewLiteNode(ctx context.Context, config *Protocol, databaseName string) (peer.Node, error) {
	bootstrapParam, err := utils.ConvertBootstrap(config.Peers, config.DevMode)
	if err != nil {
		return nil, fmt.Errorf("getting bootstrap perms in NewLiteNode failed with: %s", err)
	}

	node, err := peer.NewLitePublic(
		ctx,
		path.Join(config.Root, databaseName),
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
