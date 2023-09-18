package http

import (
	"time"

	streamClient "github.com/taubyte/p2p/streams/client"
)

type Client struct {
	client   *streamClient.Client
	defaults Parameters
	callback func() Parameters
}

type Parameters struct {
	Threshold int
	Timeout   time.Duration
}

type Option func(c *Client) error
