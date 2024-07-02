package client

import (
	streamClient "github.com/taubyte/tau/p2p/streams/client"
)

type Client struct {
	streamClient *streamClient.Client
}

func (c *Client) Close() {
	c.streamClient.Close()
}
