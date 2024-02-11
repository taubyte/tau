package monkey

import (
	"context"

	"github.com/ipfs/go-log/v2"
	iface "github.com/taubyte/go-interfaces/services/monkey"
	"github.com/taubyte/p2p/peer"
	client "github.com/taubyte/p2p/streams/client"

	protocolCommon "github.com/taubyte/tau/protocols/common"
)

var logger = log.Logger("tau.monkey.p2p.client")

var _ iface.Client = &Client{}

type Client struct {
	client *client.Client
}

func (c *Client) Close() {
	c.client.Close()
}

func New(ctx context.Context, node peer.Node) (*Client, error) {
	var (
		c   Client
		err error
	)

	c.client, err = client.New(node, protocolCommon.MonkeyProtocol)
	if err != nil {
		logger.Error("API client creation failed:", err)
		return nil, err
	}
	return &c, nil
}

/* Peer */

type Peer struct {
	Client
	Id string
}
