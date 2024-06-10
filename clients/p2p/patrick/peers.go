package patrick

import (
	peerCore "github.com/libp2p/go-libp2p/core/peer"
)

func (c *Client) Peers(pids ...peerCore.ID) *Client {
	return &Client{
		Client: c.Client,
		node:   c.node,
		peers:  pids,
	}
}
