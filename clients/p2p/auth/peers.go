package auth

import (
	peerCore "github.com/libp2p/go-libp2p/core/peer"
)

func (c *Client) Peers(pids ...peerCore.ID) *Client {
	return &Client{
		client: c.client,
		peers:  pids,
	}
}
