package hoarder

import (
	iface "github.com/taubyte/tau/core/services/hoarder"
	client "github.com/taubyte/tau/p2p/streams/client"
)

var _ iface.Client = &Client{}

type Client struct {
	*client.Client
}

type Peer struct {
	Client
	Id string
}
