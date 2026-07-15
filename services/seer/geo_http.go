package seer

import (
	"strconv"

	iface "github.com/taubyte/tau/core/services/seer"
	http "github.com/taubyte/tau/pkg/http"
	servicesCommon "github.com/taubyte/tau/services/common"
)

func (srv *Service) getGeoAllHTTPHandler(ctx http.Context) (interface{}, error) {
	resp, err := srv.geo.getAllNodes(ctx.Request().Context())
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"nodes": resp["peers"],
	}, nil
}

func (srv *Service) getGeoDistanceHTTPHandler(ctx http.Context) (interface{}, error) {
	getNumber := func(key string) (float32, error) {
		_str, err := ctx.GetStringVariable(key)
		if err != nil {
			return 0, err
		}
		_val, err := strconv.ParseFloat(_str, 32)
		if err != nil {
			return 0, err
		}

		return float32(_val), nil
	}

	_distance, err := getNumber("distance")
	if err != nil {
		return nil, err
	}

	_latitude, err := getNumber("latitude")
	if err != nil {
		return nil, err
	}

	_longitude, err := getNumber("longitude")
	if err != nil {
		return nil, err
	}

	resp, err := srv.geo.getNodes(ctx.Request().Context(), iface.Location{Latitude: _latitude, Longitude: _longitude}, _distance)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"nodes": resp["peers"],
	}, nil
}

func (srv *Service) setupLocationHTTPRoutes() {
	hosts := srv.config.RouteHosts(servicesCommon.Seer)
	srv.http.GET(&http.RouteDefinition{
		Hosts: hosts,
		Path:  "/geo/all",
		Vars: http.Variables{
			Required: []string{},
		},
		Scope:   []string{"geo/query/all"},
		Handler: srv.getGeoAllHTTPHandler,
	})

	srv.http.GET(&http.RouteDefinition{
		Hosts: hosts,
		Path:  "/geo/distance/{distance}/{latitude}/{longitude}",
		Vars: http.Variables{
			Required: []string{"distance", "latitude", "longitude"},
		},
		Scope:   []string{"geo/query"},
		Handler: srv.getGeoDistanceHTTPHandler,
	})
}
