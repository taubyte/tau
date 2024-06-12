package patrick

import (
	peerCore "github.com/libp2p/go-libp2p/core/peer"
	iface "github.com/taubyte/tau/core/services/patrick"
)

func (c *Client) Peers(pids ...peerCore.ID) iface.Client {
	return &Client{
		Client: c.Client,
		node:   c.node,
		peers:  pids,
	}
}
