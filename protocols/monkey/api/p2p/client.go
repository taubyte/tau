package p2p

import (
	"context"
	"fmt"
	"time"

	moody "bitbucket.org/taubyte/go-moody-blues"
	client "bitbucket.org/taubyte/p2p/streams/client"
	moodyCommon "github.com/taubyte/go-interfaces/moody"
	peer "github.com/taubyte/go-interfaces/p2p/peer"
	iface "github.com/taubyte/go-interfaces/services/monkey"
	common "github.com/taubyte/odo/protocols/monkey/common"
)

var (
	MinPeers                 = 0
	MaxPeers                 = 2
	DefaultGeoBeaconInterval = 5 * time.Minute
	logger, _                = moody.New("monkey.p2p.client")
)

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

	c.client, err = client.New(ctx, node, nil, common.Protocol, MinPeers, MaxPeers)
	if err != nil {
		logger.Error(moodyCommon.Object{"msg": fmt.Sprintf("API client creation failed: %s", err.Error())})
		return nil, err
	}
	return &c, nil
}

/* Peer */

type Peer struct {
	Client
	Id string
}
