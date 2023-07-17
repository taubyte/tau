package p2p

import (
	client "bitbucket.org/taubyte/p2p/streams/client"
	peer "github.com/taubyte/go-interfaces/p2p/peer"
)

type Client struct {
	node   peer.Node
	client *client.Client
	cache  *cache
}
