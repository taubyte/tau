package p2p

import (
	"context"
	"fmt"

	moody "bitbucket.org/taubyte/go-moody-blues"
	client "bitbucket.org/taubyte/p2p/streams/client"
	moodyCommon "github.com/taubyte/go-interfaces/moody"
	"github.com/taubyte/go-interfaces/p2p/peer"
	iface "github.com/taubyte/go-interfaces/services/billing"
	common "github.com/taubyte/odo/protocols/billing/common"
)

var _ iface.Client = &Client{}

var (
	MinPeers  = 0
	MaxPeers  = 2
	logger, _ = moody.New("billing.service")
)

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

	return &c, nil
}

func (c *Client) Close() {
	c.client.Close()
}

/* Peer */

type Peer struct {
	Client
	Id string
}
