package client

import (
	"context"

	"github.com/taubyte/tau/p2p/peer"
	streamClient "github.com/taubyte/tau/p2p/streams/client"
	"github.com/taubyte/tau/services/substrate/components/database/globals/p2p/common"
)

func New(ctx context.Context, node peer.Node) (client *Client, err error) {
	client = &Client{}
	if client.streamClient, err = streamClient.New(node, common.StreamProtocol); err != nil {
		return nil, err
	}

	return
}
