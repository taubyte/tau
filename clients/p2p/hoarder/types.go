package hoarder

import (
	peerCore "github.com/libp2p/go-libp2p/core/peer"
	iface "github.com/taubyte/tau/core/services/hoarder"
	client "github.com/taubyte/tau/p2p/streams/client"
)

var _ iface.Client = &Client{}

type Client struct {
	*client.Client
	peers []peerCore.ID
}

type Peer struct {
	Client
	Id string
}
