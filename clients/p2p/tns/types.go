package p2p

import (
	"github.com/taubyte/p2p/peer"
	client "github.com/taubyte/p2p/streams/client"
)

type Client struct {
	node   peer.Node
	client *client.Client
	cache  *cache
}
