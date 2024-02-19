package patrick

import (
	iface "github.com/taubyte/go-interfaces/services/patrick"
	"github.com/taubyte/p2p/peer"
	client "github.com/taubyte/p2p/streams/client"
)

var _ iface.Client = &Client{}

type Client struct {
	*client.Client
	node peer.Node
}

type Peer struct {
	Client
	Id string
}
