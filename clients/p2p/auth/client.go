package p2p

import (
	"context"
	"fmt"

	moody "bitbucket.org/taubyte/go-moody-blues"
	client "bitbucket.org/taubyte/p2p/streams/client"
	moodyCommon "github.com/taubyte/go-interfaces/moody"
	peer "github.com/taubyte/go-interfaces/p2p/peer"
	iface "github.com/taubyte/go-interfaces/services/auth"
	common "github.com/taubyte/odo/protocols/auth/common"
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

func New(ctx context.Context, node peer.Node) (*Client, error) {
	var (
		c   Client
		err error
	)
	c.client, err = client.New(ctx, node, nil, common.Protocol, MinPeers, MaxPeers)
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
