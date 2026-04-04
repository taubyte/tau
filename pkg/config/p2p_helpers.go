package config

import (
	"context"
	"fmt"
	"time"

	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/utils"
)

var WaitForSwamDuration = 10 * time.Second

func NewNode(ctx context.Context, c Config, storagePath string) (peer.Node, error) {
	if c.DevMode() {
		return NewLiteNode(ctx, c, storagePath)
	}

	bootstrapParam, err := utils.ConvertBootstrap(c.Peers(), c.DevMode())
	if err != nil {
		return nil, fmt.Errorf("getting bootstrap params in NewNode failed with: %s", err)
	}

	peerNode, err := peer.NewPublic(
		ctx,
		storagePath,
		c.PrivateKey(),
		c.SwarmKey(),
		c.P2PListen(),
		c.P2PAnnounce(),
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

func NewLiteNode(ctx context.Context, c Config, storagePath string) (peer.Node, error) {
	bootstrapParam, err := utils.ConvertBootstrap(c.Peers(), c.DevMode())
	if err != nil {
		return nil, fmt.Errorf("getting bootstrap perms in NewLiteNode failed with: %s", err)
	}

	node, err := peer.NewLitePublic(
		ctx,
		storagePath,
		c.PrivateKey(),
		c.SwarmKey(),
		c.P2PListen(),
		c.P2PAnnounce(),
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
