package p2p

import (
	"context"

	"github.com/ipfs/go-log/v2"
	iface "github.com/taubyte/go-interfaces/services/auth"
	peer "github.com/taubyte/p2p/peer"
	client "github.com/taubyte/p2p/streams/client"

	protocolCommon "github.com/taubyte/odo/protocols/common"
)

var (
	MinPeers = 2
	MaxPeers = 4
	logger   = log.Logger("auth.api.p2p")
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
		logger.Errorf("API client creation failed: %w", err)
		return nil, err
	}

	logger.Info("API client Created!")
	return &c, nil
}

func (c *Client) Close() {
	c.client.Close()
}
