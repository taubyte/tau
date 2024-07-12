package tns

import (
	peerCore "github.com/libp2p/go-libp2p/core/peer"
	tns "github.com/taubyte/tau/core/services/tns"
)

func (c *Client) Peers(pids ...peerCore.ID) tns.Client {
	return &Client{
		client: c.client,
		node:   c.node,
		peers:  pids,
		cache:  c.cache,
	}
}
