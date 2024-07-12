package auth

import (
	peerCore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/core/services/auth"
)

func (c *Client) Peers(pids ...peerCore.ID) auth.Client {
	return &Client{
		client: c.client,
		peers:  pids,
	}
}
