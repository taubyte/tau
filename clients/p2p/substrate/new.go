package substrate

import (
	"context"
	"fmt"

	"github.com/taubyte/tau/core/services/substrate"
	"github.com/taubyte/tau/p2p/peer"
	streamClient "github.com/taubyte/tau/p2p/streams/client"
	protocolCommon "github.com/taubyte/tau/services/common"
)

func New(ctx context.Context, node peer.Node, ops ...Option) (client substrate.ProxyClient, err error) {
	c := &Client{
		defaults: Parameters{
			Timeout:   DefaultTimeOut,
			Threshold: DefaultThreshold,
		},
	}
	if c.client, err = streamClient.New(node, protocolCommon.SubstrateProtocol); err != nil {
		return nil, fmt.Errorf("creating new stream client failed with: %w", err)
	}

	for _, op := range ops {
		if err = op(c); err != nil {
			c.Close()
			return nil, fmt.Errorf("running options failed with: %w", err)
		}
	}

	return c, nil
}
