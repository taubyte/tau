package substrate

import (
	"context"
	"fmt"

	// "github.com/taubyte/go-interfaces/services/substrate"
	"github.com/taubyte/p2p/peer"
	streamClient "github.com/taubyte/p2p/streams/client"
	protocolCommon "github.com/taubyte/tau/protocols/common"
)

func New(ctx context.Context, node peer.Node) (client *Client /*substrate.Client*/, err error) {
	c := &Client{}
	if c.client, err = streamClient.New(node, protocolCommon.SubstrateProtocol); err != nil {
		return nil, fmt.Errorf("creating new stream client failed with: %w", err)
	}

	return c, nil
}
