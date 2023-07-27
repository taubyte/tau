package patrick

import (
	"context"

	iface "github.com/taubyte/go-interfaces/services/patrick"
	"github.com/taubyte/p2p/peer"
	client "github.com/taubyte/p2p/streams/client"
	protocolsCommon "github.com/taubyte/tau/protocols/common"
)

func New(ctx context.Context, node peer.Node) (iface.Client, error) {
	var (
		c   Client
		err error
	)
	if c.client, err = client.New(ctx, node, nil, protocolsCommon.PatrickProtocol, MinPeers, MaxPeers); err != nil {
		logger.Error("API client creation failed:", err)
		return nil, err
	}

	return &c, nil
}

func (c *Client) Close() {
	c.client.Close()
}
