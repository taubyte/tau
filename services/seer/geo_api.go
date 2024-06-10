package seer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
	iface "github.com/taubyte/tau/core/services/seer"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	servicesCommon "github.com/taubyte/tau/services/common"
	"github.com/taubyte/utils/maps"
)

func parseLocationFromBody(body command.Body, key string) (iface.Location, error) {
	var loc iface.Location
	_loc, ok := body[key]
	if !ok {
		return loc, errors.New("Request missing " + key)
	}

	// hack: marshal then unmarshal to get it into a struct
	bloc, err := cbor.Marshal(_loc)
	if err != nil {
		return loc, fmt.Errorf("marshalling location failed with %s", err)
	}

	err = cbor.Unmarshal(bloc, &loc)
	if err != nil {
		return loc, fmt.Errorf("un-marshalling location failed with %s", err)
	}

	return loc, nil
}

func (geo *geoService) locationServiceHandler(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	action, err := maps.String(body, "action")
	if err != nil {
		return nil, err
	}

	switch action {
	case "query-all":
		return geo.getAllNodes(ctx)
	case "query":
		distance, err := maps.Number(body, "distance")
		if err != nil {
			return nil, err
		}

		from, err := parseLocationFromBody(body, "from")
		if err != nil {
			return nil, err
		}

		return geo.getNodes(ctx, from, distance)
	case "set":
		loc, err := parseLocationFromBody(body, "location")
		if err != nil {
			return nil, err
		}

		id := conn.RemotePeer().String()

		ploc, err := geo.setNode(ctx, id, loc)
		if err != nil {
			return nil, err
		}

		// Send ip's of services to all seer to store
		nodeData := &nodeData{
			Cid: id,
			Geo: ploc,
		}

		nodeBytes, err := cbor.Marshal(nodeData)
		if err != nil {
			return nil, fmt.Errorf("failed marshalling node %s with %v", id, err)
		}

		err = geo.node.PubSubPublish(ctx, servicesCommon.OraclePubSubPath, nodeBytes)
		if err != nil {
			return nil, fmt.Errorf("sending node `%s` from seer `%s` over pubsub failed with: %s", id, geo.node.ID(), err)
		}

		return cr.Response{}, nil
	default:
		return nil, errors.New("Geo action `" + action + "` not reconized.")
	}
}

func (geo *geoService) setNode(ctx context.Context, id string, location iface.Location) (*iface.PeerLocation, error) {
	loc := iface.PeerLocation{Timestamp: time.Now().Unix(), Location: location}
	_loc, err := cbor.Marshal(&loc)
	if err != nil {
		return nil, err
	}

	err = geo.ds.Put(ctx, datastore.NewKey("/geo/node/id").Instance(id), _loc)
	if err != nil {
		return nil, err
	}

	return &loc, nil
}

func (geo *geoService) getAllNodes(ctx context.Context) (cr.Response, error) {
	result, err := geo.ds.Query(
		ctx,
		query.Query{Prefix: "/geo/node", KeysOnly: false},
	)
	if err != nil {
		return nil, err
	}

	peers := make(map[string]iface.PeerLocation)
	for entry := range result.Next() {
		key := datastore.NewKey(entry.Key)
		if key.Type() != "id" {
			continue
		}

		loc := iface.PeerLocation{}
		err := cbor.Unmarshal(entry.Value, &loc)
		if err != nil {
			continue
		}

		peers[key.Name()] = loc
	}

	response := make(cr.Response)
	response["peers"] = peers

	return response, nil
}

// distance in meters
func (geo *geoService) getNodes(ctx context.Context, from iface.Location, distance float32) (cr.Response, error) {
	result, err := geo.ds.Query(
		ctx,
		query.Query{Prefix: "/geo/node", KeysOnly: false},
	)
	if err != nil {
		return nil, err
	}

	peers := make(map[string]iface.PeerLocation)
	for entry := range result.Next() {
		loc := iface.PeerLocation{}
		err := cbor.Unmarshal(entry.Value, loc)
		if err != nil {
			continue
		}

		_distance := computeDistance(
			from.Latitude,
			from.Longitude,
			loc.Location.Latitude,
			loc.Location.Longitude,
		)

		if _distance <= distance {
			peers[datastore.NewKey(entry.Key).Name()] = loc
		}
	}

	response := make(cr.Response)
	response["peers"] = peers
	return response, nil
}
