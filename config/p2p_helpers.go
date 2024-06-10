package config

import (
	"context"
	"fmt"
	"time"

	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/utils"
)

var WaitForSwamDuration = 10 * time.Second

func NewNode(ctx context.Context, config *Node, storagePath string) (peer.Node, error) {
	if config.DevMode {
		return NewLiteNode(ctx, config, storagePath)
	}

	bootstrapParam, err := utils.ConvertBootstrap(config.Peers, config.DevMode)
	if err != nil {
		return nil, fmt.Errorf("getting bootstrap perms in NewNode failed with: %s", err)
	}

	peerNode, err := peer.NewPublic(
		ctx,
		storagePath,
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

func NewLiteNode(ctx context.Context, config *Node, storagePath string) (peer.Node, error) {
	bootstrapParam, err := utils.ConvertBootstrap(config.Peers, config.DevMode)
	if err != nil {
		return nil, fmt.Errorf("getting bootstrap perms in NewLiteNode failed with: %s", err)
	}

	node, err := peer.NewLitePublic(
		ctx,
		storagePath,
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
