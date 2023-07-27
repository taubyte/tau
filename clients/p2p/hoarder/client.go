package p2p

import (
	"context"

	"github.com/taubyte/p2p/peer"
	client "github.com/taubyte/p2p/streams/client"
	protocolCommon "github.com/taubyte/tau/protocols/common"
)

func New(ctx context.Context, node peer.Node) (*Client, error) {
	var (
		c   Client
		err error
	)

	if c.client, err = client.New(ctx, node, nil, protocolCommon.HoarderProtocol, MinPeers, MaxPeers); err != nil {
		logger.Error("API client creation failed:", err.Error())
		return nil, err
	}
	return &c, nil
}

func (c *Client) Close() {
	c.client.Close()
}
