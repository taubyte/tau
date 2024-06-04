package auth

import (
	"context"

	peer "github.com/taubyte/p2p/peer"
	client "github.com/taubyte/p2p/streams/client"

	protocolCommon "github.com/taubyte/tau/services/common"
)

func New(ctx context.Context, node peer.Node) (*Client, error) {
	var (
		c   Client
		err error
	)
	c.client, err = client.New(node, protocolCommon.AuthProtocol)
	if err != nil {
		logger.Error("API client creation failed:", err.Error())
		return nil, err
	}

	logger.Info("API client Created!")
	return &c, nil
}

func (c *Client) Close() {
	c.client.Close()
}
