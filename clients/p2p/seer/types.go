package p2p

import (
	client "bitbucket.org/taubyte/p2p/streams/client"
	iface "github.com/taubyte/go-interfaces/services/seer"
)

type Client struct {
	client   *client.Client
	services iface.Services
}

func (c *Client) Close() {
	c.client.Close()
	c.services = nil
}
