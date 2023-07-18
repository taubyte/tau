package p2p

import (
	"context"
	"fmt"

	client "bitbucket.org/taubyte/p2p/streams/client"
	logging "github.com/ipfs/go-log/v2"
	peer "github.com/taubyte/go-interfaces/p2p/peer"
	iface "github.com/taubyte/go-interfaces/services/auth"

	protocolCommon "github.com/taubyte/odo/protocols/common"
)

var (
	MinPeers = 2
	MaxPeers = 4
	logger   = logging.Logger("auth.api.p2p")
)

var _ iface.Client = &Client{}

type Client struct {
	client *client.Client
}

func New(ctx context.Context, node peer.Node) (*Client, error) {
	var (
		c   Client
		err error
	)
	c.client, err = client.New(ctx, node, nil, protocolCommon.AuthProtocol, MinPeers, MaxPeers)
	if err != nil {
		logger.Error(fmt.Sprintf("API client creation failed: %s", err.Error()))
		return nil, err
	}

	logger.Debug(fmt.Sprintf("message: API client Created!"))
	return &c, nil
}

func (c *Client) Close() {
	c.client.Close()
}
