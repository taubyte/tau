package p2p

import (
	iface "github.com/taubyte/go-interfaces/services/seer"
	client "github.com/taubyte/p2p/streams/client"
)

type Client struct {
	client   *client.Client
	services iface.Services
}

func (c *Client) Close() {
	c.client.Close()
	c.services = nil
}
