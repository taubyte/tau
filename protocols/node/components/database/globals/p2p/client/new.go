package client

import (
	"context"

	"github.com/taubyte/odo/protocols/node/components/database/globals/p2p/common"
	"github.com/taubyte/p2p/peer"
	streamClient "github.com/taubyte/p2p/streams/client"
)

var (
	MinPeers = 0
	MaxPeers = 4
)

func New(ctx context.Context, node peer.Node) (client *Client, err error) {
	client = &Client{}
	if client.streamClient, err = streamClient.New(ctx, node, nil, common.StreamProtocol, MinPeers, MaxPeers); err != nil {
		return nil, err
	}

	return
}
