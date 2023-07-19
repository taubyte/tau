package p2p

import (
	"context"
	"errors"
	"fmt"
	"time"

	moody "bitbucket.org/taubyte/go-moody-blues"
	moodyCommon "github.com/taubyte/go-interfaces/moody"
	iface "github.com/taubyte/go-interfaces/services/patrick"
	protocolsCommon "github.com/taubyte/odo/protocols/common"
	"github.com/taubyte/p2p/peer"
	client "github.com/taubyte/p2p/streams/client"
)

var (
	MinPeers                 = 0
	MaxPeers                 = 5
	DefaultGeoBeaconInterval = 5 * time.Minute
	ErrorGeoBeaconStoped     = errors.New("GeoBeacon Stopped")
	logger, _                = moody.New("patrick.p2p.client")
)

var _ iface.Client = &Client{}

type Client struct {
	client *client.Client
}

func New(ctx context.Context, node *peer.Node) (iface.Client, error) {
	var (
		c   Client
		err error
	)
	if c.client, err = client.New(ctx, node, nil, protocolsCommon.Patrick, MinPeers, MaxPeers); err != nil {
		logger.Error(moodyCommon.Object{"msg": fmt.Sprintf("API client creation failed: %s", err.Error())})
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
