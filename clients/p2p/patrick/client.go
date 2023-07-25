package patrick

import (
	"context"

	iface "github.com/taubyte/go-interfaces/services/patrick"
	protocolsCommon "github.com/taubyte/odo/protocols/common"
	"github.com/taubyte/p2p/peer"
	client "github.com/taubyte/p2p/streams/client"
)

func New(ctx context.Context, node peer.Node) (iface.Client, error) {
	var (
		c   Client
		err error
	)
	if c.client, err = client.New(ctx, node, nil, protocolsCommon.PatrickProtocol, MinPeers, MaxPeers); err != nil {
		logger.Errorf("API client creation failed: %w", err)
		return nil, err
	}

	return &c, nil
}

func (c *Client) Close() {
	c.client.Close()
}
