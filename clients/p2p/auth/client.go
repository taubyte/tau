package p2p

import (
	"context"
	"fmt"

	moody "bitbucket.org/taubyte/go-moody-blues"
	moodyCommon "github.com/taubyte/go-interfaces/moody"
	iface "github.com/taubyte/go-interfaces/services/auth"
	peer "github.com/taubyte/p2p/peer"
	client "github.com/taubyte/p2p/streams/client"

	protocolCommon "github.com/taubyte/odo/protocols/common"
)

var (
	MinPeers  = 2
	MaxPeers  = 4
	logger, _ = moody.New("auth.api.p2p")
)

var _ iface.Client = &Client{}

type Client struct {
	client *client.Client
}

func New(ctx context.Context, node *peer.Node) (*Client, error) {
	var (
		c   Client
		err error
	)
	c.client, err = client.New(ctx, node, nil, protocolCommon.AuthProtocol, MinPeers, MaxPeers)
	if err != nil {
		logger.Error(moodyCommon.Object{"message": fmt.Sprintf("API client creation failed: %s", err.Error())})
		return nil, err
	}

	logger.Debug(moodyCommon.Object{"message": "API client Created!"})
	return &c, nil
}

func (c *Client) Close() {
	c.client.Close()
}
