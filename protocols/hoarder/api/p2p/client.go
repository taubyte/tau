package p2p

import (
	"context"
	"errors"
	"fmt"
	"time"

	moodyBlues "bitbucket.org/taubyte/go-moody-blues"
	client "bitbucket.org/taubyte/p2p/streams/client"
	"github.com/taubyte/go-interfaces/moody"
	peer "github.com/taubyte/go-interfaces/p2p/peer"
	iface "github.com/taubyte/go-interfaces/services/hoarder"
	common "github.com/taubyte/odo/protocols/hoarder/common"
)

var _ iface.Client = &Client{}

var (
	MinPeers                 = 0
	MaxPeers                 = 2
	DefaultGeoBeaconInterval = 5 * time.Minute
	ErrorGeoBeaconStopped    = errors.New("geoBeacon Stopped")
	logger                   moody.Logger
)

type Client struct {
	client *client.Client
}

func init() {
	logger, _ = moodyBlues.New("hoarder.p2p.client")
}

func New(ctx context.Context, node peer.Node) (*Client, error) {
	var (
		c   Client
		err error
	)

	if c.client, err = client.New(ctx, node, nil, common.Protocol, MinPeers, MaxPeers); err != nil {
		logger.Errorf(fmt.Sprintf("API client creation failed: %v", err))
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
