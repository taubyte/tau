package seer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	iface "github.com/taubyte/go-interfaces/services/seer"
	"github.com/taubyte/p2p/streams"
	"github.com/taubyte/p2p/streams/command"
	cr "github.com/taubyte/p2p/streams/command/response"
	"github.com/taubyte/utils/maps"
)

func parseLocationfromBody(body command.Body, key string) (iface.Location, error) {
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

		from, err := parseLocationfromBody(body, "from")
		if err != nil {
			return nil, err
		}

		return geo.getNodes(ctx, from, distance)
	case "set":
		loc, err := parseLocationfromBody(body, "location")
		if err != nil {
			return nil, err
		}

		return geo.setNode(ctx, conn, loc)
	default:
		return nil, errors.New("Geo action `" + action + "` not reconized.")
	}
}

func (geo *geoService) setNode(ctx context.Context, conn streams.Connection, location iface.Location) (cr.Response, error) {
	loc := iface.PeerLocation{Timestamp: time.Now().Unix(), Location: location}
	_loc, err := cbor.Marshal(&loc)
	if err != nil {
		return nil, err
	}

	err = geo.seer.db.Put(ctx, "/geo/"+conn.RemotePeer().Pretty(), _loc)
	if err != nil {
		return nil, err
	}

	return cr.Response{}, nil
}

func (geo *geoService) getAllNodes(ctx context.Context) (cr.Response, error) {
	// FIXME: Use Async. Looks like Async does not work well when there is no result
	//        probably kvdb needs to close the chan
	resp, err := geo.seer.db.List(ctx, "/geo/")
	if err != nil {
		return nil, err
	}

	peers := make(map[string]iface.PeerLocation)
	for _, path := range resp {
		_loc, err := geo.seer.db.Get(ctx, path)
		if err != nil {
			continue
		}

		loc := iface.PeerLocation{}
		err = cbor.Unmarshal(_loc, &loc)
		if err != nil {
			continue
		}

		peers[strings.TrimPrefix(path, "/geo/")] = loc
	}

	response := make(cr.Response)
	response["peers"] = peers
	return response, nil
}

// distance in meters
func (geo *geoService) getNodes(ctx context.Context, from iface.Location, distance float32) (cr.Response, error) {
	// FIXME: Use Async. Looks like Async does not work well when there is no result
	//        probably kvdb needs to close the chan
	resp, err := geo.seer.db.List(ctx, "/geo/")
	if err != nil {
		return nil, err
	}

	peers := make(map[string]iface.PeerLocation)
	for _, path := range resp {
		_loc, err := geo.seer.db.Get(ctx, path)
		if err != nil {
			continue
		}

		loc := iface.PeerLocation{}
		err = cbor.Unmarshal(_loc, &loc)
		if err != nil {
			continue
		}

		_distance := computeDistance(from.Latitude, from.Longitude, loc.Location.Latitude, loc.Location.Longitude)

		if _distance <= distance {
			_loc, err := geo.seer.db.Get(ctx, path)
			if err != nil {
				continue
			}

			loc := iface.PeerLocation{}
			err = cbor.Unmarshal(_loc, &loc)
			if err != nil {
				continue
			}

			peers[strings.TrimPrefix(path, "/geo/")] = loc
		}
	}

	response := make(cr.Response)
	response["peers"] = peers
	return response, nil
}
