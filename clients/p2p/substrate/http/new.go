package http

import (
	"context"
	"fmt"

	"github.com/taubyte/go-interfaces/services/substrate/components/http"
	"github.com/taubyte/p2p/peer"
	streamClient "github.com/taubyte/p2p/streams/client"
	protocolCommon "github.com/taubyte/tau/protocols/common"
)

func New(ctx context.Context, node peer.Node, ops ...Option) (client http.Client, err error) {
	c := &Client{}
	if c.client, err = streamClient.New(node, protocolCommon.SubstrateHttpProtocol); err != nil {
		return nil, fmt.Errorf("creating new stream client failed with: %w", err)
	}

	for _, op := range ops {
		if err = op(c); err != nil {
			return nil, fmt.Errorf("running options failed with: %w", err)
		}
	}

	return c, nil
}
