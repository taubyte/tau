package client

import (
	"context"

	streamClient "bitbucket.org/taubyte/p2p/streams/client"
	"github.com/taubyte/go-interfaces/p2p/peer"
	"github.com/taubyte/odo/protocols/node/components/database/globals/p2p/common"
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
