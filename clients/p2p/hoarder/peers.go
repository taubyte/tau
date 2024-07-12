package hoarder

import (
	peerCore "github.com/libp2p/go-libp2p/core/peer"
	hoarder "github.com/taubyte/tau/core/services/hoarder"
)

func (c *Client) Peers(pids ...peerCore.ID) hoarder.Client {
	return &Client{
		Client: c.Client,
		peers:  pids,
	}
}
