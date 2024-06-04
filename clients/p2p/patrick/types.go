package patrick

import (
	"github.com/taubyte/p2p/peer"
	client "github.com/taubyte/p2p/streams/client"
	iface "github.com/taubyte/tau/core/services/patrick"
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
