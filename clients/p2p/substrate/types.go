package substrate

import (
	"time"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	streamClient "github.com/taubyte/tau/p2p/streams/client"
)

type Client struct {
	client   *streamClient.Client
	defaults Parameters
	callback func() Parameters
	peers    []peerCore.ID
}

type Parameters struct {
	Threshold int
	Timeout   time.Duration
}

type Option func(c *Client) error
