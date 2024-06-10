package seer

import (
	"context"

	iface "github.com/taubyte/tau/core/services/seer"
	client "github.com/taubyte/tau/p2p/streams/client"
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
