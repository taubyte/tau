package patrick

import (
	"context"

	iface "github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/p2p/peer"
	client "github.com/taubyte/tau/p2p/streams/client"
	servicesCommon "github.com/taubyte/tau/services/common"
)

func New(ctx context.Context, node peer.Node) (iface.Client, error) {
	c := &Client{
		node: node,
	}

	var err error
	if c.Client, err = client.New(c.node, servicesCommon.PatrickProtocol); err != nil {
		logger.Error("API client creation failed:", err)
		return nil, err
	}

	return c, nil
}

func (c *Client) Close() {
	c.Client.Close()
}
