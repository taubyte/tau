package seer

import (
	peerCore "github.com/libp2p/go-libp2p/core/peer"
	iface "github.com/taubyte/tau/core/services/seer"
)

func (c *Client) Peers(pids ...peerCore.ID) iface.Client {
	return &Client{
		client:   c.client,
		services: c.services,
		peers:    pids,
	}
}
