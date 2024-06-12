package monkey

import (
	peerCore "github.com/libp2p/go-libp2p/core/peer"
	monkey "github.com/taubyte/tau/core/services/monkey"
)

func (c *Client) Peers(pids ...peerCore.ID) monkey.Client {
	return &Client{
		client: c.client,
		peers:  pids,
	}
}
