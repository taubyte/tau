package seer

import (
	"context"
	"fmt"
	"time"

	"github.com/fxamacker/cbor/v2"

	iface "github.com/taubyte/tau/core/services/seer"
	"github.com/taubyte/tau/p2p/peer"
	streamClient "github.com/taubyte/tau/p2p/streams/client"
	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/tau/p2p/streams/command/response"
	"github.com/taubyte/utils/maps"

	"github.com/taubyte/tau/services/common"
)

func New(ctx context.Context, node peer.Node) (client iface.Client, err error) {
	c := &Client{}
	c.client, err = streamClient.New(node, common.SeerProtocol)
	if err != nil {
		logger.Error("API client creation failed: %s", err)
		return
	}

	c.services = make(iface.Services, 0)
	return c, nil
}

func (c *Client) Geo() iface.Geo {
	return (*Geo)(c)
}

func (g *Geo) newPeer(id string, loc iface.PeerLocation) *iface.Peer {
	return &iface.Peer{
		Id:       id,
		Location: loc,
	}
}

func (g *Geo) newPeerList(response response.Response) ([]*iface.Peer, error) {
	__peers, ok := response["peers"]
	if !ok {
		return nil, fmt.Errorf("no `peers` found in %v", response)
	}

	_peers, ok := __peers.(map[interface{}]interface{})
	if !ok {
		return nil, fmt.Errorf("processing `peers` of type %T", __peers)
	}

	peers, err := maps.ToStringKeys(_peers)
	if err != nil {
		return nil, fmt.Errorf("processing `peers` returned %s", err)
	}

	ret := make([]*iface.Peer, 0)
	for _id, _loc := range peers {
		loc := iface.PeerLocation{}

		// hack: marshal then unmarshal to get it into a struct
		bloc, err := cbor.Marshal(_loc)
		if err == nil {
			if err = cbor.Unmarshal(bloc, &loc); err == nil {
				ret = append(ret, g.newPeer(_id, loc))
			}
		}

	}

	return ret, nil
}

func (g *Geo) All() ([]*iface.Peer, error) {
	response, err := g.client.Send("geo", command.Body{"action": "query-all"}, g.peers...)
	if err != nil {
		return nil, fmt.Errorf("provider replied with %s", err)
	}

	return g.newPeerList(response)
}

// distance is in meter
func (g *Geo) Distance(from iface.Location, distance float32) ([]*iface.Peer, error) {
	response, err := g.client.Send("geo", command.Body{"action": "query", "from": from, "distance": distance}, g.peers...)
	if err != nil {
		return nil, err
	}

	return g.newPeerList(response)
}

func (g *Geo) Set(location iface.Location) (err error) {
	_, err = g.client.Send("geo", command.Body{"action": "set", "location": location}, g.peers...)
	return err
}

func (g *Geo) Beacon(location iface.Location) iface.GeoBeacon {
	ctx, ctx_cancel := context.WithCancel(g.client.Context())
	return &GeoBeacon{
		ctx:        ctx,
		ctx_cancel: ctx_cancel,
		geo:        g,
		_status:    make(chan error, 16),
		location:   location,
	}
}

func (b *GeoBeacon) updateLocation() error {
	return b.geo.Set(b.location)
}

// clean up status for better memory management
func (b *GeoBeacon) cleanStatus() {
	defer close(b._status)
	for {
		select {
		case <-b._status:
		default:
			return
		}
	}
}

func (b *GeoBeacon) Start() {
	go func() {
		var err error

		// First update as soon as we start
		err = b.updateLocation()
		if err != nil {
			b._status <- err
		}

		for {
			select {
			case <-b.ctx.Done():
				b.cleanStatus()
				b.status = ErrorGeoBeaconStopped
				return
			case <-time.After(DefaultGeoBeaconInterval):
				if err = b.updateLocation(); err != nil {
					b._status <- err
				}
			case err = <-b._status:
				b.status = err
			}
		}
	}()
}

func (b *GeoBeacon) Status() error {
	return b.status
}

func (b *GeoBeacon) Stop() {
	b.ctx_cancel()
}

func (c *Client) Close() {
	c.client.Close()
	c.services = nil
}
