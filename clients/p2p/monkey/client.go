package monkey

import (
	"context"

	"github.com/ipfs/go-log/v2"
	iface "github.com/taubyte/tau/core/services/monkey"
	"github.com/taubyte/tau/p2p/peer"
	client "github.com/taubyte/tau/p2p/streams/client"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	protocolCommon "github.com/taubyte/tau/services/common"
)

var logger = log.Logger("tau.monkey.p2p.client")

var _ iface.Client = &Client{}

type Client struct {
	client *client.Client
	peers  []peerCore.ID
}

func (c *Client) Close() {
	c.client.Close()
}

func New(ctx context.Context, node peer.Node) (iface.Client, error) {
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
