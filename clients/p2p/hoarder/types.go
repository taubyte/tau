package hoarder

import (
	client "github.com/taubyte/p2p/streams/client"
	iface "github.com/taubyte/tau/core/services/hoarder"
)

var _ iface.Client = &Client{}

type Client struct {
	*client.Client
}

type Peer struct {
	Client
	Id string
}
