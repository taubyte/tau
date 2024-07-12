package hoarder

import (
	"context"

	hoarder "github.com/taubyte/tau/core/services/hoarder"
	"github.com/taubyte/tau/p2p/peer"
	client "github.com/taubyte/tau/p2p/streams/client"
	protocolCommon "github.com/taubyte/tau/services/common"
)

func New(ctx context.Context, node peer.Node) (hoarder.Client, error) {
	var (
		c   Client
		err error
	)

	if c.Client, err = client.New(node, protocolCommon.HoarderProtocol); err != nil {
		logger.Error("API client creation failed:", err.Error())
		return nil, err
	}
	return &c, nil
}

func (c *Client) Close() {
	c.Client.Close()
}
