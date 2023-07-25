package auth

import (
	"context"

	peer "github.com/taubyte/p2p/peer"
	client "github.com/taubyte/p2p/streams/client"

	protocolCommon "github.com/taubyte/odo/protocols/common"
)

func New(ctx context.Context, node peer.Node) (*Client, error) {
	var (
		c   Client
		err error
	)
	c.client, err = client.New(ctx, node, nil, protocolCommon.AuthProtocol, MinPeers, MaxPeers)
	if err != nil {
		logger.Errorf("API client creation failed: %w", err)
		return nil, err
	}

	logger.Info("API client Created!")
	return &c, nil
}

func (c *Client) Close() {
	c.client.Close()
}
