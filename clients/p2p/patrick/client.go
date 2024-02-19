package patrick

import (
	"context"

	iface "github.com/taubyte/go-interfaces/services/patrick"
	"github.com/taubyte/p2p/peer"
	client "github.com/taubyte/p2p/streams/client"
	protocolsCommon "github.com/taubyte/tau/protocols/common"
)

func New(ctx context.Context, node peer.Node) (iface.Client, error) {
	c := &Client{
		node: node,
	}

	var err error
	if c.Client, err = client.New(c.node, protocolsCommon.PatrickProtocol); err != nil {
		logger.Error("API client creation failed:", err)
		return nil, err
	}

	return c, nil
}

func (c *Client) Close() {
	c.Client.Close()
}
