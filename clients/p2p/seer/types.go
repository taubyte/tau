package seer

import (
	"context"

	client "github.com/taubyte/p2p/streams/client"
	iface "github.com/taubyte/tau/core/services/seer"
)

var _ iface.Client = &Client{}

type Client struct {
	client   *client.Client
	services iface.Services
}

type Peer struct {
	Id       string
	Location iface.PeerLocation
}

type Geo Client

type GeoBeacon struct {
	ctx        context.Context
	ctx_cancel context.CancelFunc
	geo        *Geo
	location   iface.Location
	status     error
	_status    chan error
}
