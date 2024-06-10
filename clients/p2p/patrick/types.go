package patrick

import (
	iface "github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/p2p/peer"
	client "github.com/taubyte/tau/p2p/streams/client"
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
